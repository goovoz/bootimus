package server

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bootimus/internal/bsdboot"
	"bootimus/internal/models"
)

const bsdSelectionTTL = 15 * time.Minute

type bsdSelection struct {
	cacheDir string
	distro   string
	expires  time.Time
}

type bsdSelections struct {
	mu      sync.Mutex
	byIP    map[string]bsdSelection
	now     func() time.Time
	ttl     time.Duration
	pruneAt time.Time
}

func newBSDSelections() *bsdSelections {
	return &bsdSelections{byIP: map[string]bsdSelection{}, now: time.Now, ttl: bsdSelectionTTL}
}

func (b *bsdSelections) record(ip, cacheDir, distro string) {
	if ip == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if now.After(b.pruneAt) {
		for key, sel := range b.byIP {
			if now.After(sel.expires) {
				delete(b.byIP, key)
			}
		}
		b.pruneAt = now.Add(b.ttl)
	}
	b.byIP[ip] = bsdSelection{cacheDir: cacheDir, distro: distro, expires: now.Add(b.ttl)}
}

func (b *bsdSelections) lookup(ip string) (bsdSelection, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sel, ok := b.byIP[ip]
	if !ok || b.now().After(sel.expires) {
		return bsdSelection{}, false
	}
	return sel, true
}

func hostOf(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

func (s *Server) bsdCacheRoot(cacheDir string) (string, bool) {
	if s.config == nil || s.config.ISODir == "" || cacheDir == "" {
		return "", false
	}
	isoRoot := filepath.Clean(s.config.ISODir)
	root := filepath.Join(isoRoot, filepath.FromSlash(cacheDir), bsdboot.CacheSubdir)
	if !strings.HasPrefix(filepath.Clean(root), isoRoot+string(os.PathSeparator)) {
		return "", false
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return root, true
}

func (s *Server) bsdImageForCache(cacheDir string) *models.Image {
	if s.config == nil || s.config.Storage == nil {
		return nil
	}
	images, err := s.config.Storage.ListImages()
	if err != nil {
		return nil
	}
	for i := range images {
		if strings.TrimSuffix(images[i].Filename, filepath.Ext(images[i].Filename)) == cacheDir {
			return images[i]
		}
	}
	return nil
}

func (s *Server) bsdDistroForCache(cacheDir, root string) string {
	if img := s.bsdImageForCache(cacheDir); img != nil && bsdboot.IsBSD(img.Distro) {
		return img.Distro
	}
	if _, err := os.Stat(filepath.Join(root, bsdboot.OpenBSDKernel)); err == nil {
		return bsdboot.DistroOpenBSD
	}
	return bsdboot.DistroNetBSD
}

func (s *Server) bsdTFTPHost() string {
	host := s.config.ServerAddr
	if host == "" {
		host = "${next-server}"
	}
	if s.config.TFTPPort != 0 && s.config.TFTPPort != 69 {
		return fmt.Sprintf("%s:%d", host, s.config.TFTPPort)
	}
	return host
}

func validBSDPathParts(parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (s *Server) resolveBSDRequest(cleanPath, remoteIP string) (root, cacheDir, distro, file string, err error) {
	if strings.HasPrefix(cleanPath, bsdboot.TFTPPrefix) {
		parts := strings.Split(strings.TrimPrefix(cleanPath, bsdboot.TFTPPrefix), "/")
		if !validBSDPathParts(parts) {
			return "", "", "", "", fmt.Errorf("invalid BSD boot path: %s", cleanPath)
		}
		for i := len(parts) - 1; i >= 1; i-- {
			candidate := strings.Join(parts[:i], "/")
			if r, ok := s.bsdCacheRoot(candidate); ok {
				return r, candidate, s.bsdDistroForCache(candidate, r), strings.Join(parts[i:], "/"), nil
			}
		}
		return "", "", "", "", fmt.Errorf("no extracted BSD image for %s", cleanPath)
	}

	sel, ok := s.bsdSelections.lookup(remoteIP)
	if !ok {
		return "", "", "", "", fmt.Errorf("no BSD boot selected for %s", remoteIP)
	}
	for _, name := range bsdboot.RootFiles(sel.distro) {
		if name == cleanPath {
			r, ok := s.bsdCacheRoot(sel.cacheDir)
			if !ok {
				return "", "", "", "", fmt.Errorf("BSD boot files missing for %s", sel.cacheDir)
			}
			return r, sel.cacheDir, sel.distro, bsdboot.RootFileTarget(sel.distro, name), nil
		}
	}
	return "", "", "", "", fmt.Errorf("file not found: %s", cleanPath)
}

func isBSDRootFile(cleanPath string) bool {
	for _, distro := range []string{bsdboot.DistroOpenBSD, bsdboot.DistroNetBSD} {
		for _, name := range bsdboot.RootFiles(distro) {
			if name == cleanPath {
				return true
			}
		}
	}
	return false
}

func (s *Server) bsdBootConfig(cacheDir, distro string) []byte {
	kernel := bsdboot.TFTPPrefix + cacheDir + "/" + bsdboot.KernelName(distro, "")
	if distro == bsdboot.DistroNetBSD {
		if root, ok := s.bsdCacheRoot(cacheDir); ok {
			if _, err := os.Stat(filepath.Join(root, bsdboot.NetBSDKernel)); err != nil {
				kernel = bsdboot.TFTPPrefix + cacheDir + "/" + bsdboot.NetBSDKernelPlain
			}
		}
	}
	params := ""
	if img := s.bsdImageForCache(cacheDir); img != nil {
		params = img.BootParams
	}
	return []byte(bsdboot.BootConfig(distro, kernel, params))
}

func (s *Server) serveBSDTFTP(cleanPath, remote string, rf io.ReaderFrom) (bool, error) {
	if s.bsdSelections == nil {
		return false, nil
	}
	if !strings.HasPrefix(cleanPath, bsdboot.TFTPPrefix) && !isBSDRootFile(cleanPath) {
		return false, nil
	}
	root, cacheDir, distro, file, err := s.resolveBSDRequest(cleanPath, hostOf(remote))
	if err != nil {
		return true, err
	}

	if file == bsdboot.OpenBSDRandomSeed {
		seed := make([]byte, bsdboot.RandomSeedSize())
		if _, err := rand.Read(seed); err != nil {
			return true, err
		}
		log.Printf("TFTP: Serving generated %s random seed to %s", bsdboot.DisplayName(distro), remote)
		return true, sendTFTPBytes(rf, seed)
	}
	if bsdboot.IsConfig(distro, file) {
		log.Printf("TFTP: Serving generated %s boot configuration for %s to %s", bsdboot.DisplayName(distro), cacheDir, remote)
		return true, sendTFTPBytes(rf, s.bsdBootConfig(cacheDir, distro))
	}

	full := filepath.Join(root, filepath.FromSlash(file))
	if !strings.HasPrefix(filepath.Clean(full), root+string(os.PathSeparator)) {
		return true, fmt.Errorf("invalid BSD boot path: %s", cleanPath)
	}
	f, err := os.Open(full)
	if err != nil {
		return true, fmt.Errorf("file not found: %s", cleanPath)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return true, fmt.Errorf("file not found: %s", cleanPath)
	}
	log.Printf("TFTP: Serving %s boot file %s from %s to %s", bsdboot.DisplayName(distro), file, cacheDir, remote)
	if rfs, ok := rf.(interface{ SetSize(int64) error }); ok {
		rfs.SetSize(info.Size())
	}
	_, err = rf.ReadFrom(f)
	return true, err
}

func sendTFTPBytes(rf io.ReaderFrom, data []byte) error {
	if rfs, ok := rf.(interface{ SetSize(int64) error }); ok {
		rfs.SetSize(int64(len(data)))
	}
	_, err := rf.ReadFrom(bytes.NewReader(data))
	return err
}

func (s *Server) handleBSDBoot(w http.ResponseWriter, r *http.Request) {
	rel, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/bsd/"))
	if err != nil || !strings.HasSuffix(rel, "/boot.ipxe") {
		http.NotFound(w, r)
		return
	}
	cacheDir := path.Clean(strings.TrimSuffix(rel, "/boot.ipxe"))
	if cacheDir == "." || cacheDir == "/" || strings.HasPrefix(cacheDir, "..") || strings.HasPrefix(cacheDir, "/") {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	root, ok := s.bsdCacheRoot(cacheDir)
	if !ok {
		http.NotFound(w, r)
		return
	}
	distro := s.bsdDistroForCache(cacheDir, root)
	mac := normaliseMenuMAC(r.URL.Query().Get("mac"))
	platform := r.URL.Query().Get("platform")
	buildarch := r.URL.Query().Get("buildarch")

	loader := bsdboot.BIOSLoader(distro)
	firmware := "BIOS"
	if platform == "efi" {
		loader = bsdboot.EFILoader(distro, buildarch)
		firmware = "UEFI"
	}

	w.Header().Set("Content-Type", "text/plain")
	if _, err := os.Stat(filepath.Join(root, loader)); err != nil {
		s.logAndBroadcast("Client %s: %s %s loader %s is not present in %s", mac, bsdboot.DisplayName(distro), firmware, loader, cacheDir)
		fmt.Fprintf(w, "#!ipxe\necho %s %s loader %s was not found in this image\necho Boot this client with the other firmware mode or use an ISO that ships %s\nprompt --timeout 10000 Press any key to return to the menu...\nexit 0\n", bsdboot.DisplayName(distro), firmware, loader, loader)
		return
	}

	ip := hostOf(r.RemoteAddr)
	s.bsdSelections.record(ip, cacheDir, distro)
	s.recordBootIfNew(mac, cacheDir+"/"+bsdboot.CacheSubdir+"/"+loader, r.RemoteAddr)
	s.logAndBroadcast("Client %s (IP: %s): booting %s via %s over TFTP from %s", mac, ip, bsdboot.DisplayName(distro), loader, cacheDir)

	tftpURL := fmt.Sprintf("tftp://%s/%s%s/%s", s.bsdTFTPHost(), bsdboot.TFTPPrefix, encodePathSegments(cacheDir), loader)
	fmt.Fprintf(w, "#!ipxe\necho Loading %s %s loader over TFTP...\nchain %s || goto bsdfail\nexit 0\n:bsdfail\necho Failed to load %s\nprompt --timeout 10000 Press any key to return to the menu...\nexit 0\n", bsdboot.DisplayName(distro), firmware, tftpURL, tftpURL)
}
