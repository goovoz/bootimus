package autoinstall

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

const (
	TypeAutounattend = "autounattend"
	TypePreseed      = "preseed"
	TypeKickstart    = "kickstart"
	TypeAutoinstall  = "autoinstall"
	TypeAutoYaST     = "autoyast"
	TypeAgama        = "agama"
	TypeIgnition     = "ignition"
	TypeProxmox      = "proxmox"
	TypeGeneric      = "generic"
)

const (
	DistroOpenSUSE = "opensuse"
	DistroProxmox  = "proxmox"
	DistroFlatcar  = "flatcar"
)

func FileType(distro, filename string) string {
	distro = strings.ToLower(distro)
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".xml":
		if distro == DistroOpenSUSE {
			return TypeAutoYaST
		}
		return TypeAutounattend
	case ".cfg":
		return TypePreseed
	case ".ks":
		return TypeKickstart
	case ".yaml", ".yml":
		return TypeAutoinstall
	case ".json", ".jsonnet":
		if distro == DistroOpenSUSE {
			return TypeAgama
		}
		return TypeGeneric
	case ".ign":
		return TypeIgnition
	case ".toml":
		if distro == DistroProxmox {
			return TypeProxmox
		}
		return TypeGeneric
	default:
		return TypeGeneric
	}
}

func PathType(rel string) string {
	rel = filepath.ToSlash(rel)
	if i := strings.Index(rel, "/"); i >= 0 {
		return FileType(rel[:i], rel[i+1:])
	}
	return FileType("", rel)
}

func ContentType(scriptType string) string {
	switch scriptType {
	case TypeAutounattend, TypeAutoYaST:
		return "application/xml; charset=utf-8"
	case TypeAutoinstall:
		return "text/yaml; charset=utf-8"
	case TypeAgama, TypeIgnition:
		return "application/json; charset=utf-8"
	case TypeProxmox:
		return "application/toml; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}

type proxmoxSystemInfo struct {
	NetworkInterfaces []struct {
		Link string `json:"link"`
		MAC  string `json:"mac"`
	} `json:"network_interfaces"`
}

func ProxmoxInterfaceMACs(body io.Reader) ([]string, error) {
	var info proxmoxSystemInfo
	if err := json.NewDecoder(body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode proxmox system information: %w", err)
	}
	var macs []string
	for _, nic := range info.NetworkInterfaces {
		mac := NormaliseMAC(nic.MAC)
		if mac != "" {
			macs = append(macs, mac)
		}
	}
	if len(macs) == 0 {
		return nil, fmt.Errorf("proxmox system information lists no network interface mac addresses")
	}
	return macs, nil
}

func NormaliseMAC(mac string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(mac), "-", ":"))
}
