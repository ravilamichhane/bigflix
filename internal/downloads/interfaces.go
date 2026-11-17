package downloads

import (
	"context"
	"fmt"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/utp"
	"net"
	"strconv"
)

// Register all custom sockets before adding torrents. No wildcard listeners or
// default dialers are created when explicit interface selection is saved.
func (m *Manager) openInterfaceSockets() {
	for _, binding := range m.bindings {
		iface, err := net.InterfaceByName(binding.Name)
		if err != nil {
			m.networkErrors = append(m.networkErrors, binding.Name+": unavailable")
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil || iface.Flags&net.FlagUp == 0 {
			m.networkErrors = append(m.networkErrors, binding.Name+": down or unavailable")
			continue
		}
		opened := 0
		port := binding.Port
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip.IsLinkLocalUnicast() {
				continue
			}
			family := "4"
			if ip.To4() == nil {
				family = "6"
			}
			lc := net.ListenConfig{Control: interfaceControl(iface.Index)}
			tcp, err := lc.Listen(context.Background(), "tcp"+family, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
			if err != nil {
				m.networkErrors = append(m.networkErrors, fmt.Sprintf("%s (%s): %v", binding.Name, ip, err))
				continue
			}
			actual := tcp.Addr().(*net.TCPAddr).Port
			pc, err := lc.ListenPacket(context.Background(), "udp"+family, net.JoinHostPort(ip.String(), strconv.Itoa(actual)))
			if err != nil {
				tcp.Close()
				m.networkErrors = append(m.networkErrors, fmt.Sprintf("%s UDP: %v", binding.Name, err))
				continue
			}
			socket, err := utp.NewSocketFromPacketConn(pc)
			if err != nil {
				tcp.Close()
				pc.Close()
				m.networkErrors = append(m.networkErrors, err.Error())
				continue
			}
			ds, err := m.client.NewAnacrolixDhtServer(socket)
			if err != nil {
				tcp.Close()
				socket.Close()
				m.networkErrors = append(m.networkErrors, err.Error())
				continue
			}
			m.client.AddListener(tcp)
			m.client.AddListener(socket)
			m.client.AddDialer(torrent.NetworkDialer{Network: "tcp" + family, Dialer: &net.Dialer{LocalAddr: &net.TCPAddr{IP: ip}, Control: interfaceControl(iface.Index)}})
			m.client.AddDialer(torrent.NetworkDialer{Network: "udp" + family, Dialer: socket})
			m.client.AddDhtServer(torrent.AnacrolixDhtServerWrapper{Server: ds})
			m.networkClosers = append(m.networkClosers, func() { ds.Close(); tcp.Close(); socket.Close() })
			port = actual
			opened++
		}
		if opened == 0 {
			m.networkErrors = append(m.networkErrors, binding.Name+": no usable listener; check addresses and port availability")
		}
	}
}
