package mulery

import (
	"fmt"
	"net"
	"strings"
)

// AllowedIPs determines who make requests.
type AllowedIPs struct {
	Input []string
	Nets  []*net.IPNet
}

var _ = fmt.Stringer(AllowedIPs{})

// String turns a list of allowedIPs into a printable masterpiece.
func (n AllowedIPs) String() string {
	if len(n.Nets) < 1 {
		return "(none)"
	}

	s := ""

	for i := range n.Nets {
		if s != "" {
			s += ", "
		}

		s += n.Nets[i].String()
	}

	return s
}

// Contains returns true if an IP is allowed.
func (n AllowedIPs) Contains(ip string) bool {
	ip = strings.Trim(ip[:strings.LastIndex(ip, ":")], "[]")

	for i := range n.Nets {
		if n.Nets[i].Contains(net.ParseIP(ip)) {
			return true
		}
	}

	return false
}

// MakeIPs turns a list of CIDR strings (or plain IPs) into a list of net.IPNet.
// This "allowed" list is later used to check incoming IPs from web requests.
func MakeIPs(upstreams []string) AllowedIPs {
	a := AllowedIPs{
		Input: make([]string, len(upstreams)),
		Nets:  []*net.IPNet{},
	}

	for idx, ipAddr := range upstreams {
		a.Input[idx] = ipAddr

		if !strings.Contains(ipAddr, "/") {
			if strings.Contains(ipAddr, ":") {
				ipAddr += "/128"
			} else {
				ipAddr += "/32"
			}
		}

		if _, i, err := net.ParseCIDR(ipAddr); err == nil {
			a.Nets = append(a.Nets, i)
		}
	}

	return a
}
