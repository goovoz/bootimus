package nfs

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bootimus/internal/netbind"

	billy "github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	gonfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"
)

const (
	DiskImageExport  = "disk-images"
	DiskImageStaging = ".staging"
)

type Server struct {
	rootDir       string
	port          int
	bindInterface string
	listener      net.Listener
	DiskImageDir  string
}

func NewServer(rootDir string, port int, bindInterface string) *Server {
	return &Server{
		rootDir:       rootDir,
		port:          port,
		bindInterface: bindInterface,
	}
}

func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	listener, err := netbind.Listen(s.bindInterface, "tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to start NFS server: %w", err)
	}

	s.listener = listener
	log.Printf("NFS server listening on %s (export root %s)", addr, s.rootDir)
	if s.DiskImageDir != "" {
		log.Printf("NFS server exporting disk images from %s as /%s", s.DiskImageDir, DiskImageExport)
	}

	cache := nfshelper.NewCachingHandler(newHandler(s.rootDir, s.DiskImageDir), 1024)
	return gonfs.Serve(listener, cache)
}

func (s *Server) Stop() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

type bootHandler struct {
	gonfs.Handler
	root     billy.Filesystem
	diskRoot billy.Filesystem
}

func newHandler(rootDir, diskImageDir string) *bootHandler {
	root := osfs.New(rootDir)
	h := &bootHandler{
		Handler: nfshelper.NewNullAuthHandler(root),
		root:    root,
	}
	if diskImageDir != "" {
		h.diskRoot = osfs.New(diskImageDir)
	}
	return h
}

type readOnlyFS struct {
	billy.Filesystem
}

type writableFS struct {
	billy.Filesystem
}

func (fs *readOnlyFS) Capabilities() billy.Capability {
	return billy.ReadCapability | billy.SeekCapability
}

func (fs *readOnlyFS) Create(string) (billy.File, error) { return nil, os.ErrPermission }

func (fs *readOnlyFS) OpenFile(name string, flag int, perm os.FileMode) (billy.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0 {
		return nil, os.ErrPermission
	}
	return fs.Filesystem.OpenFile(name, flag, perm)
}

func (fs *readOnlyFS) Remove(string) error                { return os.ErrPermission }
func (fs *readOnlyFS) Rename(string, string) error        { return os.ErrPermission }
func (fs *readOnlyFS) MkdirAll(string, os.FileMode) error { return os.ErrPermission }
func (fs *readOnlyFS) Symlink(string, string) error       { return os.ErrPermission }
func (fs *readOnlyFS) TempFile(string, string) (billy.File, error) {
	return nil, os.ErrPermission
}

func (fs *readOnlyFS) Chroot(path string) (billy.Filesystem, error) {
	sub, err := fs.Filesystem.Chroot(path)
	if err != nil {
		return nil, err
	}
	return &readOnlyFS{sub}, nil
}

func (fs *writableFS) Chroot(path string) (billy.Filesystem, error) {
	sub, err := fs.Filesystem.Chroot(path)
	if err != nil {
		return nil, err
	}
	return &writableFS{sub}, nil
}

func (h *bootHandler) Mount(ctx context.Context, conn net.Conn, req gonfs.MountRequest) (gonfs.MountStatus, billy.Filesystem, []gonfs.AuthFlavor) {
	sub := strings.Trim(strings.ReplaceAll(string(req.Dirpath), "\\", "/"), "/")
	fs, err := h.export(sub)
	if err != nil {
		log.Printf("NFS: mount rejected for %q: %v", sub, err)
		return gonfs.MountStatusErrNoEnt, nil, nil
	}
	return gonfs.MountStatusOk, fs, []gonfs.AuthFlavor{gonfs.AuthFlavorNull}
}

func (h *bootHandler) export(sub string) (billy.Filesystem, error) {
	if sub == DiskImageExport || strings.HasPrefix(sub, DiskImageExport+"/") {
		return h.diskImageExport(strings.TrimPrefix(strings.TrimPrefix(sub, DiskImageExport), "/"))
	}
	if sub == "" {
		return &readOnlyFS{h.root}, nil
	}
	fs, err := h.root.Chroot(sub)
	if err != nil {
		return nil, err
	}
	return &readOnlyFS{fs}, nil
}

func (h *bootHandler) diskImageExport(rest string) (billy.Filesystem, error) {
	if h.diskRoot == nil {
		return nil, fmt.Errorf("disk image export is not configured")
	}
	if rest == "" {
		return &readOnlyFS{h.diskRoot}, nil
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && parts[0] == DiskImageStaging && parts[1] != "" && !strings.HasPrefix(parts[1], ".") {
		if _, err := h.diskRoot.Stat(rest); err != nil {
			return nil, fmt.Errorf("no capture staging area %q", parts[1])
		}
		fs, err := h.diskRoot.Chroot(rest)
		if err != nil {
			return nil, err
		}
		return &writableFS{fs}, nil
	}
	return nil, fmt.Errorf("disk image path %q is not exported", rest)
}

func (fs *writableFS) hostPath(name string) string {
	return filepath.Join(fs.Root(), filepath.Clean("/"+name))
}

func (fs *writableFS) Chmod(name string, mode os.FileMode) error {
	return os.Chmod(fs.hostPath(name), mode)
}

func (fs *writableFS) Lchown(name string, uid, gid int) error {
	return os.Lchown(fs.hostPath(name), uid, gid)
}

func (fs *writableFS) Chown(name string, uid, gid int) error {
	return os.Chown(fs.hostPath(name), uid, gid)
}

func (fs *writableFS) Chtimes(name string, atime, mtime time.Time) error {
	return os.Chtimes(fs.hostPath(name), atime, mtime)
}

func (h *bootHandler) Change(fs billy.Filesystem) billy.Change {
	if w, ok := fs.(*writableFS); ok {
		return w
	}
	return nil
}
