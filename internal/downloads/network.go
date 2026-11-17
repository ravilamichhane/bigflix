package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sort"
	"strconv"
)

type NetworkInterface struct {
	Name      string
	Up        bool
	Addresses []string
	Selected  bool
	Port      int
	Listeners []string
}
type InterfaceBinding struct {
	Name string
	Port int
}
type NetworkSettings struct {
	All        bool
	Port       int
	ActualPort int
	Interfaces []NetworkInterface
	Errors     []string
}

func (m *Manager) NetworkSettings() (NetworkSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := NetworkSettings{Port: m.listenPort}
	out.All = len(m.bindings) == 0
	out.Errors = append(out.Errors, m.networkErrors...)
	for _, listener := range m.client.Listeners() {
		_, port, err := net.SplitHostPort(listener.Addr().String())
		if err == nil {
			out.ActualPort, _ = strconv.Atoi(port)
			break
		}
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return out, err
	}
	for _, iface := range interfaces {
		item := NetworkInterface{Name: iface.Name, Up: iface.Flags&net.FlagUp != 0}
		item.Port = m.listenPort
		for _, binding := range m.bindings {
			if binding.Name == iface.Name {
				item.Selected = true
				item.Port = binding.Port
			}
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return out, err
		}
		for _, address := range addresses {
			item.Addresses = append(item.Addresses, address.String())
			ip, _, _ := net.ParseCIDR(address.String())
			if out.All && item.Up && ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				item.Selected = true
			}
			for _, listener := range m.client.Listeners() {
				host, _, err := net.SplitHostPort(listener.Addr().String())
				if err == nil && net.ParseIP(host).Equal(ip) {
					item.Listeners = append(item.Listeners, listener.Addr().Network()+" "+listener.Addr().String())
				}
			}
		}
		out.Interfaces = append(out.Interfaces, item)
	}
	for _, binding := range m.bindings {
		found := false
		for _, item := range out.Interfaces {
			if item.Name == binding.Name {
				found = true
			}
		}
		if !found {
			out.Interfaces = append(out.Interfaces, NetworkInterface{Name: binding.Name, Selected: true, Port: binding.Port})
		}
	}
	sort.Slice(out.Interfaces, func(i, j int) bool { return out.Interfaces[i].Name < out.Interfaces[j].Name })
	return out, nil
}
func (m *Manager) SaveInterfaceSettings(ctx context.Context, bindings []InterfaceBinding) error {
	if len(bindings) == 0 {
		return errors.New("select at least one interface")
	}
	seen := map[string]bool{}
	for _, b := range bindings {
		if seen[b.Name] {
			return errors.New("duplicate interface")
		}
		seen[b.Name] = true
		if _, err := net.InterfaceByName(b.Name); err != nil {
			return errors.New("unknown network interface")
		}
		if b.Port != 0 && (b.Port < 1024 || b.Port > 65535) {
			return errors.New("ports must be 1024–65535, or dynamic")
		}
	}
	b, err := json.Marshal(bindings)
	if err != nil {
		return err
	}
	return m.store.SaveNetworkConfig(ctx, b)
}
func (m *Manager) SaveNetworkSettings(ctx context.Context, port int) error {
	if port != 0 && (port < 1024 || port > 65535) {
		return errors.New("use a fixed port between 1024 and 65535, or dynamic mode")
	}
	return m.store.SetTorrentPort(ctx, port)
}
