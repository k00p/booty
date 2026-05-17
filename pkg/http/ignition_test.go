package http

import (
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeefy/booty/pkg/config"
	"github.com/spf13/viper"
)

func writeTestData(t *testing.T) string {
	t.Helper()

	dataDir := t.TempDir()
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed creating config dir: %v", err)
	}

	hardwareJSON := `{"aa:bb:cc:dd:ee:ff":{"mac":"aa:bb:cc:dd:ee:ff","hostname":"node1"}}`
	if err := os.WriteFile(filepath.Join(dataDir, "hardware.json"), []byte(hardwareJSON), 0o644); err != nil {
		t.Fatalf("failed writing hardware map: %v", err)
	}

	ignitionTemplate := `variant: fcos
version: 1.5.0
`
	if err := os.WriteFile(filepath.Join(configDir, "ignition.yaml"), []byte(ignitionTemplate), 0o644); err != nil {
		t.Fatalf("failed writing ignition template: %v", err)
	}

	return dataDir
}

func configureTestViper(dataDir string) {
	viper.Set(config.DataDir, dataDir)
	viper.Set(config.HardwareMap, "hardware.json")
	viper.Set(config.IgnitionFile, "config/ignition.yaml")
	viper.Set(config.ServerIP, "127.0.0.1")
	viper.Set(config.ServerHttpPort, "80")
	viper.Set(config.HttpPort, "8080")
	viper.Set(config.JoinString, "")
	viper.Set("debug", false)
}

func TestHandleIgnitionRequestMacResolutionPaths(t *testing.T) {
	originalPing := arpingPing
	defer func() {
		arpingPing = originalPing
	}()

	testCases := []struct {
		name         string
		target       string
		remoteAddr   string
		pingFunc     func(ip net.IP) (net.HardwareAddr, time.Duration, error)
		expectReboot bool
	}{
		{
			name:       "explicit mac override success",
			target:     "/ignition.json?mac=aa:bb:cc:dd:ee:ff",
			remoteAddr: "192.0.2.10:2345",
			pingFunc: func(ip net.IP) (net.HardwareAddr, time.Duration, error) {
				return nil, 0, fmt.Errorf("arp should not be called when mac query exists")
			},
			expectReboot: false,
		},
		{
			name:       "arp fallback success",
			target:     "/ignition.json",
			remoteAddr: "192.0.2.11:2345",
			pingFunc: func(ip net.IP) (net.HardwareAddr, time.Duration, error) {
				return net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}, 0, nil
			},
			expectReboot: false,
		},
		{
			name:         "unknown mac override returns reboot config",
			target:       "/ignition.json?mac=11:22:33:44:55:66",
			remoteAddr:   "192.0.2.12:2345",
			pingFunc:     originalPing,
			expectReboot: true,
		},
		{
			name:       "arp timeout without mac returns reboot config",
			target:     "/ignition.json",
			remoteAddr: "192.0.2.13:2345",
			pingFunc: func(ip net.IP) (net.HardwareAddr, time.Duration, error) {
				return nil, 0, fmt.Errorf("timeout")
			},
			expectReboot: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := writeTestData(t)
			configureTestViper(dataDir)
			arpingPing = tc.pingFunc

			req := httptest.NewRequest("GET", tc.target, nil)
			req.RemoteAddr = tc.remoteAddr
			rr := httptest.NewRecorder()

			handleIgnitionRequest(rr, req)

			body := rr.Body.String()
			hasRebootMarker := strings.Contains(body, "Reboot now please")
			if hasRebootMarker != tc.expectReboot {
				t.Fatalf("unexpected reboot config response. expectReboot=%v body=%s", tc.expectReboot, body)
			}
		})
	}
}
