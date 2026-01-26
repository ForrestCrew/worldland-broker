package sdk

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// DetectPublicIP detects the public IP address of the current machine.
// It tries multiple services for reliability.
func DetectPublicIP(ctx context.Context) (string, error) {
	services := []string{
		"https://ifconfig.me/ip",
		"https://api.ipify.org",
		"https://ipinfo.io/ip",
		"https://checkip.amazonaws.com",
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	for _, service := range services {
		ip, err := fetchIP(ctx, client, service)
		if err == nil && ip != "" {
			return ip, nil
		}
	}

	// Fallback: try to detect from local network interfaces
	return detectLocalPublicIP()
}

func fetchIP(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	ip := strings.TrimSpace(string(body))

	// Validate it looks like an IP
	if net.ParseIP(ip) != nil {
		return ip, nil
	}

	return "", nil
}

func detectLocalPublicIP() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range interfaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			// Skip loopback and private IPs
			if ip == nil || ip.IsLoopback() || ip.IsPrivate() {
				continue
			}

			// Return first public IPv4
			if ip.To4() != nil {
				return ip.String(), nil
			}
		}
	}

	return "", nil
}
