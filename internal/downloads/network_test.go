package downloads

import (
	"bigflix/internal/database"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

func loopbackInterface(t *testing.T) string {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			return iface.Name
		}
	}
	t.Fatal("no loopback interface")
	return ""
}

func TestTwoInterfacesDifferentPorts(t *testing.T) {
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	var other string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ip, _, _ := net.ParseCIDR(addr.String())
			if ip.To4() != nil {
				other = iface.Name
				break
			}
		}
		if other != "" {
			break
		}
	}
	if other == "" {
		t.Skip("requires a second addressed interface")
	}
	db, err := database.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bindings := []InterfaceBinding{{Name: loopbackInterface(t), Port: 0}, {Name: other, Port: 0}}
	b, _ := json.Marshal(bindings)
	if err = db.SaveNetworkConfig(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	m, err := Open(db, Options{MediaDir: filepath.Join(t.TempDir(), "cache")})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s, err := m.NetworkSettings()
	if err != nil || len(s.Errors) > 0 {
		t.Fatalf("listeners: %+v %v", s, err)
	}
	ports := map[string]bool{}
	for _, binding := range bindings {
		found := false
		for _, item := range s.Interfaces {
			if item.Name == binding.Name {
				for _, listener := range item.Listeners {
					_, addr, ok := strings.Cut(listener, " ")
					if !ok {
						t.Fatal(listener)
					}
					_, port, _ := net.SplitHostPort(addr)
					ports[port] = true
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("no listener on %s", binding.Name)
		}
	}
	if len(ports) != 2 {
		t.Fatalf("expected independent ports: %v", ports)
	}
}

func TestInterfacePortsAndOccupiedPort(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	name := loopbackInterface(t)
	b, _ := json.Marshal([]InterfaceBinding{{Name: name, Port: 0}})
	if err = db.SaveNetworkConfig(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	opts := Options{MediaDir: filepath.Join(t.TempDir(), "cache")}
	m, err := Open(db, opts)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := m.NetworkSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.Errors) > 0 {
		t.Fatal(settings.Errors)
	}
	port := settings.ActualPort
	if port == 0 {
		t.Fatal("dynamic listener did not allocate a port")
	}
	for _, addr := range m.client.ListenAddrs() {
		host, _, _ := net.SplitHostPort(addr.String())
		if !net.ParseIP(host).IsLoopback() {
			t.Fatalf("unselected address listening: %s", addr)
		}
	}
	if err = m.SaveInterfaceSettings(context.Background(), nil); err == nil {
		t.Fatal("empty selection accepted")
	}
	if err = m.SaveInterfaceSettings(context.Background(), []InterfaceBinding{{Name: name, Port: 70000}}); err == nil {
		t.Fatal("invalid port accepted")
	}
	if err = m.SaveInterfaceSettings(context.Background(), []InterfaceBinding{{Name: name, Port: port}}); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err = Open(db, opts)
	if err != nil {
		t.Fatal(err)
	}
	settings, err = m.NetworkSettings()
	if err != nil || settings.ActualPort != port || len(settings.Errors) > 0 {
		t.Fatalf("fixed restart failed: %+v %v", settings, err)
	}
	m.Close()
	occupied, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	m, err = Open(db, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	settings, _ = m.NetworkSettings()
	if len(settings.Errors) == 0 {
		t.Fatal("occupied port was not reported")
	}
	for _, addr := range m.client.ListenAddrs() {
		host, _, _ := net.SplitHostPort(addr.String())
		if host == "0.0.0.0" || host == "::" {
			t.Fatal("fell back to wildcard")
		}
	}
}
