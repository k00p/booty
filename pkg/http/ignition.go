package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"text/template"

	butaneConfig "github.com/coreos/butane/config"
	butaneCommon "github.com/coreos/butane/config/common"
	coreOSType "github.com/coreos/ignition/v2/config/v3_5_experimental/types"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/j-keck/arping"
	"github.com/jeefy/booty/pkg/config"
	"github.com/jeefy/booty/pkg/hardware"
	"github.com/spf13/viper"
)

var arpingPing = arping.Ping

func resolveIgnitionRequestMAC(r *http.Request) (string, string, error) {
	if macAddress := strings.TrimSpace(r.URL.Query().Get("mac")); macAddress != "" {
		return macAddress, "url", nil
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "", "arp", fmt.Errorf("split remote addr: %w", err)
	}
	remoteIP := net.ParseIP(ip)
	if remoteIP == nil {
		return "", "arp", fmt.Errorf("parse remote ip from %q", ip)
	}

	hwAddr, _, err := arpingPing(remoteIP)
	if err != nil {
		return "", "arp", err
	}
	return hwAddr.String(), "arp", nil
}

func handleIgnitionRequest(w http.ResponseWriter, r *http.Request) {
	// If we don't identify the host, tell FlatCar to reboot
	// Reboot the host till we identify it
	// Cool so, we want to have logic based around a recognized MAC address
	// Therefore what we need to do is collect the MAC address

	log.Printf("Ignition Request URI: %s", r.RequestURI)

	macAddress, macSource, err := resolveIgnitionRequestMAC(r)
	if macSource == "url" {
		if viper.GetBool("debug") {
			log.Printf("Mac address url override `%s`", macAddress)
		}
	} else {
		if err != nil {
			log.Printf("Unable to resolve MAC via ARP for %s: %v", r.RemoteAddr, err)
		} else if viper.GetBool("debug") {
			log.Printf("Mac address from ARP `%s`", macAddress)
		}
	}

	if viper.GetBool("debug") {
		log.Printf("Using mac address `%s` (source=%s)", macAddress, macSource)
	}

	if macAddress == "" {
		log.Printf("No MAC resolved for ignition request (%s from %s); returning reboot ignition. ARP fallback may fail across L3, NAT/proxy, or container boundaries.", r.RequestURI, r.RemoteAddr)
	}

	var host *hardware.Host
	if macAddress != "" {
		host = hardware.GetMacAddress(macAddress)
		if host == nil {
			log.Printf("No host entry found for resolved MAC `%s`; returning reboot ignition", macAddress)
		}
	}

	var tpl bytes.Buffer

	templateData := struct {
		JoinString  string
		ServerIP    string
		OSTreeImage string
		Hostname    string
	}{
		JoinString: viper.GetString(config.JoinString),
		ServerIP:   fmt.Sprintf("%s:%s", viper.GetString(config.ServerIP), viper.GetString(config.ServerHttpPort)),
	}

	ignitionFile := viper.GetString(config.IgnitionFile)
	if host != nil {
		if host.IgnitionFile != "" {
			ignitionFile = host.IgnitionFile
		}
		templateData.Hostname = host.Hostname

		// Ingelligently rewrite what image to send to the client depending on cache state
		// First, default to the remote image location
		templateData.OSTreeImage = host.OSTreeImage

		// If we have a local image, use that instead
		if host.OSTreeImage != "" {
			localImage := fmt.Sprintf("%s:%s/%s", viper.GetString(config.ServerIP), viper.GetString(config.HttpPort), host.OSTreeImage)
			digest, err := crane.Digest(localImage)
			if err != nil {
				log.Printf("Error getting %s from cache: %s", localImage, err)
			}
			if digest == "" {
				log.Printf("Image (%s) not found in local cache yet...", localImage)
			} else {
				templateData.OSTreeImage = localImage
			}
		}
	}
	t, err := template.ParseFiles(fmt.Sprintf("%s/%s", viper.GetString(config.DataDir), ignitionFile))
	if err != nil {
		w.Write([]byte(err.Error()))
		return
	}

	err = t.Execute(&tpl, templateData)
	if err != nil {
		w.Write([]byte(err.Error()))
		return
	}

	if host == nil {
		coreosConfig := coreOSType.Config{}
		coreosConfig.Ignition.Version = "3.4.0"
		truePointer := true
		contentsPointer := `
[Service]
Type=simple
ExecStart=reboot

[Install]
WantedBy=default.target
`
		coreosConfig.Systemd.Units = append(coreosConfig.Systemd.Units, coreOSType.Unit{
			Name:     "Reboot now please",
			Enabled:  &truePointer,
			Contents: &contentsPointer,
		})
		var dataOut []byte
		dataOut, err := json.Marshal(&coreosConfig)
		if err != nil {
			w.Write([]byte(fmt.Sprintf("Failed to marshal output: %v", err)))
			return
		}
		w.Write(dataOut)
		return
	}

	ignCfg, report, err := butaneConfig.TranslateBytes(tpl.Bytes(), butaneCommon.TranslateBytesOptions{
		Pretty: true,
	})
	if err != nil {
		errMsg := fmt.Sprintf("Error parsing coreos ignition: %s", err.Error())
		log.Println(errMsg)
		log.Printf("%s", tpl.Bytes())
		for _, entry := range report.Entries {
			log.Printf("%s", entry.String())
		}
		w.Write([]byte(errMsg))
		return
	}
	if len(report.Entries) > 0 {
		errMsg := fmt.Sprintf("Problems parsing coreos ignition: %s", report.String())
		log.Println(errMsg)
		log.Printf("%s", tpl.Bytes())
		for _, entry := range report.Entries {
			log.Printf("%s", entry.String())
		}
		if report.IsFatal() {
			w.Write([]byte(errMsg))
			return

		}
	}

	w.Write(ignCfg)
}
