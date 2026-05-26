package tftp

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/j-keck/arping"
	"github.com/jeefy/booty/pkg/config"
	"github.com/jeefy/booty/pkg/hardware"
	"github.com/pin/tftp"
	"github.com/spf13/viper"
)

// readHandler is called when client starts file download from server
func readHandler(filename string, rf io.ReaderFrom) error {
	log.Printf("TFTP Get: %s\n", filename)
	raddr := rf.(tftp.OutgoingTransfer).RemoteAddr()
	laddr := rf.(tftp.RequestPacketInfo).LocalIP()
	if viper.GetBool("debug") {
		log.Println("RRQ from", raddr.String(), "To ", laddr.String())
		log.Println("")
	}

	osToLoad := "flatcar"
	menuDefault := "run-from-disk"
	customIPXEFile := ""

	if hwAddr, _, err := arping.Ping(raddr.IP); err != nil {
		log.Printf("Error with ARP request: %s", err)
	} else {
		macAddress := hwAddr.String()
		host := hardware.GetMacAddress(macAddress)
		if host != nil {
			if host.OS != "" {
				osToLoad = host.OS
			}
			if host.IPXEFile != "" {
				customIPXEFile = host.IPXEFile
			}
			if host.DoInstall {
				menuDefault = "install"
				if filename == "booty.ipxe" {
					host.DoInstall = false
					hardware.WriteMacAddress(macAddress, *host)
				}
			}
		}
	}

	urlHost := viper.GetString(config.ServerIP)
	hostPort := viper.GetInt(config.ServerHttpPort)
	if hostPort != 80 {
		urlHost = fmt.Sprintf("%s:%d", urlHost, hostPort)
	}

	if filename == "booty.ipxe" {
		if customIPXEFile != "" {
			customFilePath, err := safeDataPath(viper.GetString(config.DataDir), customIPXEFile)
			if err != nil {
				log.Printf("Error resolving custom iPXE file %q: %v", customIPXEFile, err)
				return err
			}
			rawBytes, err := os.ReadFile(customFilePath)
			if err != nil {
				log.Printf("Error reading custom iPXE file %q: %v", customIPXEFile, err)
				return err
			}
			toServe := renderIPXETemplate(string(rawBytes), urlHost, menuDefault)
			r := strings.NewReader(toServe)
			n, err := rf.ReadFrom(r)
			if err != nil {
				log.Printf("Error reading custom iPXE config: %v\n", err)
				return err
			}
			log.Printf("%d bytes sent (%s)\n", n, filename)
			return nil
		}

		toServe := renderIPXETemplate(PXEConfig[fmt.Sprintf("%s.ipxe", osToLoad)], urlHost, menuDefault)
		r := strings.NewReader(toServe)
		n, err := rf.ReadFrom(r)
		if err != nil {
			log.Printf("Error reading iPXE config: %v\n", err)
			return err
		}
		log.Printf("%d bytes sent (%s)\n", n, filename)
		return nil
	}

	if filename == "pxelinux.cfg/default" {
		r := strings.NewReader(strings.Replace(PXEConfig[osToLoad], "[[server]]", urlHost, -1))
		n, err := rf.ReadFrom(r)
		if err != nil {
			log.Printf("Error reading PXE config: %v\n", err)
			return err
		}
		log.Printf("%d bytes sent (%s)\n", n, filename)
		return nil
	}
	file, err := os.Open(fmt.Sprintf("%s/%s", viper.GetString(config.DataDir), filename))
	if err != nil {
		return err
	}
	n, err := rf.ReadFrom(file)
	if err != nil {
		return err
	}
	log.Printf("%d bytes sent (%s)\n", n, filename)
	return nil
}

func renderIPXETemplate(content, urlHost, menuDefault string) string {
	r := strings.NewReplacer(
		"[[server]]", urlHost,
		"[[menu-default]]", menuDefault,
		"[[coreos-channel]]", viper.GetString(config.CoreOSChannel),
		"[[coreos-arch]]", viper.GetString(config.CoreOSArchitecture),
		"[[coreos-version]]", viper.GetString(config.CurrentCoreOSVersion),
	)
	return r.Replace(content)
}

func safeDataPath(dataDir string, relativeFile string) (string, error) {
	cleanPath := filepath.Clean(relativeFile)
	if cleanPath == "." {
		return "", fmt.Errorf("empty relative iPXE file path")
	}
	if filepath.IsAbs(cleanPath) {
		return "", fmt.Errorf("absolute iPXE file paths are not allowed")
	}

	dataDirAbs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", fmt.Errorf("unable to resolve data directory: %w", err)
	}

	requestedPathAbs, err := filepath.Abs(filepath.Join(dataDirAbs, cleanPath))
	if err != nil {
		return "", fmt.Errorf("unable to resolve requested iPXE file path: %w", err)
	}

	dataDirEval, err := filepath.EvalSymlinks(dataDirAbs)
	if err != nil {
		return "", fmt.Errorf("unable to resolve data directory symlinks: %w", err)
	}

	requestedPathEval, err := filepath.EvalSymlinks(requestedPathAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("requested iPXE file does not exist")
		}
		return "", fmt.Errorf("unable to resolve requested iPXE file symlinks: %w", err)
	}

	relPath, err := filepath.Rel(dataDirEval, requestedPathEval)
	if err != nil {
		return "", fmt.Errorf("unable to validate requested iPXE file path: %w", err)
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("iPXE file path escapes data directory")
	}

	return requestedPathEval, nil
}

// writeHandler is called when client starts file upload to server
func writeHandler(filename string, wt io.WriterTo) error {
	log.Printf("TFTP writes are not supported: %s\n", filename)
	return nil
}

func StartTFTP() {
	// use nil in place of handler to disable read or write operations
	s := tftp.NewServer(readHandler, writeHandler)
	s.SetBlockSize(512)
	s.EnableSinglePort()
	s.SetTimeout(60 * time.Second) // optional
	go func() {
		err := s.ListenAndServe(":69") // blocks until s.Shutdown() is called
		if err != nil {
			log.Fatalf("TFTP Server error: %v\n", err)
		}
	}()
	log.Println("TFTP Server started")
}
