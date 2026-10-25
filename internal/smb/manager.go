package smb

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"
)

const startupGrace = time.Second

type Manager struct {
	dataDir    string
	interfaces string
	port       int

	mu     sync.RWMutex
	shares map[string]string

	cmd *exec.Cmd

	stateMu sync.Mutex
	runErr  error
}

func NewManager(dataDir, addr, bindInterface string, port int) *Manager {
	interfaces := bindTarget(bindInterface, addr, localIPs())
	if interfaces == "" {
		log.Printf("Windows SMB: %s is not assigned to a local interface, listening on all interfaces", addr)
	} else {
		log.Printf("Windows SMB: binding to %s", interfaces)
	}
	return &Manager{
		dataDir:    dataDir,
		interfaces: interfaces,
		port:       port,
		shares:     make(map[string]string),
	}
}

func bindTarget(bindInterface, addr string, local []net.IP) string {
	if bindInterface != "" {
		return bindInterface
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return ""
	}
	for _, l := range local {
		if l.Equal(ip) {
			return addr
		}
	}
	return ""
}

func localIPs() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		if ipNet, ok := a.(*net.IPNet); ok {
			ips = append(ips, ipNet.IP)
		}
	}
	return ips
}

func (m *Manager) AddShare(name, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shares[name] = path
}

func (m *Manager) RemoveShare(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.shares, name)
}

func (m *Manager) HasShare(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.shares[name]
	return ok
}

func (m *Manager) ShareCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.shares)
}

func (m *Manager) Port() int {
	return m.port
}

func (m *Manager) Err() error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	return m.runErr
}

func (m *Manager) Running() bool {
	return m.Err() == nil && m.cmd != nil && m.cmd.Process != nil
}

func (m *Manager) setErr(err error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if m.runErr == nil {
		m.runErr = err
	}
}

func (m *Manager) Start() error {
	err := m.start()
	if err != nil {
		m.setErr(err)
	}
	return err
}

func (m *Manager) start() error {
	smbdPath, err := exec.LookPath("smbd")
	if err != nil {
		return fmt.Errorf("smbd not found in PATH (install the samba package): %w", err)
	}

	if err := m.ensureStateDirs(); err != nil {
		return fmt.Errorf("failed to create smbd state directories: %w", err)
	}

	if err := checkPermissionSupport(m.smbDir()); err != nil {
		return err
	}

	if err := m.writeConfig(); err != nil {
		return fmt.Errorf("failed to write smb.conf: %w", err)
	}

	m.cmd = exec.Command(smbdPath, "--no-process-group", "--foreground", "--configfile", m.configPath())
	m.cmd.Stdout = os.Stdout
	m.cmd.Stderr = os.Stderr

	if err := m.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start smbd: %w", err)
	}

	exited := make(chan error, 1)
	go func(cmd *exec.Cmd) {
		err := cmd.Wait()
		if err != nil {
			log.Printf("SMB: smbd exited: %v (check %s)", err, m.logPath())
			m.setErr(fmt.Errorf("smbd exited: %v", err))
		} else {
			log.Printf("SMB: smbd exited cleanly")
			m.setErr(errors.New("smbd exited"))
		}
		exited <- err
	}(m.cmd)

	select {
	case err := <-exited:
		msg := fmt.Sprintf("smbd exited immediately (%v)", err)
		if tail := logTail(m.logPath(), 3); tail != "" {
			msg += ": " + tail
		}
		return fmt.Errorf("%s (see %s)", msg, m.logPath())
	case <-time.After(startupGrace):
	}

	log.Printf("SMB: smbd started (PID %d, port %d)", m.cmd.Process.Pid, m.port)
	return nil
}

func checkPermissionSupport(dir string) error {
	probe := filepath.Join(dir, ".permcheck")
	_ = os.RemoveAll(probe)
	if err := os.Mkdir(probe, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %w", probe, err)
	}
	defer os.Remove(probe)
	chmodErr := os.Chmod(probe, 0755)
	st, err := os.Stat(probe)
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", probe, err)
	}
	if mode := st.Mode().Perm(); mode != 0755 || chmodErr != nil {
		return fmt.Errorf("%s is on a filesystem that cannot store Unix permissions (a 0755 directory reads back as %04o) and smbd refuses to run there; move the Bootimus data directory to a native Linux filesystem such as ext4 or XFS", dir, mode)
	}
	return nil
}

func logTail(path string, lines int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	all := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	for i, l := range all {
		all[i] = strings.TrimSpace(l)
	}
	return strings.Join(all, " | ")
}

func (m *Manager) Reload() error {
	if err := m.writeConfig(); err != nil {
		return fmt.Errorf("failed to write smb.conf: %w", err)
	}
	if !m.Running() {
		return nil
	}
	ctrlPath, err := exec.LookPath("smbcontrol")
	if err != nil {
		log.Printf("SMB: warning - smbcontrol not found, cannot reload smbd: %v", err)
		return nil
	}
	out, cErr := exec.Command(ctrlPath, "--configfile", m.configPath(), "smbd", "reload-config").CombinedOutput()
	if cErr != nil {
		log.Printf("SMB: warning - smbcontrol reload-config failed: %v (%s)", cErr, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) Stop() {
	if m.Running() {
		if err := m.cmd.Process.Kill(); err != nil {
			log.Printf("SMB: warning - could not kill smbd: %v", err)
		}
	}
}

func SanitiseShareName(isoBase string) string {
	var sb strings.Builder
	for _, r := range isoBase {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	result := sb.String()
	if len(result) > 80 {
		result = result[:80]
	}
	return result
}

func (m *Manager) smbDir() string     { return filepath.Join(m.dataDir, "smb") }
func (m *Manager) configPath() string { return filepath.Join(m.smbDir(), "smb.conf") }
func (m *Manager) logPath() string    { return filepath.Join(m.smbDir(), "log", "smbd.log") }

func (m *Manager) ensureStateDirs() error {
	for _, sub := range []string{"locks", "state", "cache", "pid", "log", "ncalrpc", "private", "usershares"} {
		if err := os.MkdirAll(filepath.Join(m.smbDir(), sub), 0755); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) writeConfig() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	configTemplate := template.Must(template.New("config").Parse(`[global]
workgroup = WORKGROUP
server role = standalone server
log level = 1
log file = {{ .Dir }}/log/smbd.log
{{- if .Interfaces }}
bind interfaces only = yes
interfaces = {{ .Interfaces }}
{{- end }}
smb ports = {{ .Port }}
server min protocol = SMB2
map to guest = bad user
guest account = nobody
load printers = no
disable netbios = yes
disable spoolss = yes
# Install clients (WinPE) reboot mid-session and reconnect with the same
# IP. Without these, smbd hangs onto the prior tree connect/oplocks and
# the next net use fails. Locks aren't meaningful for a read-only share.
oplocks = no
kernel oplocks = no
level2 oplocks = no
strict locking = no
deadtime = 1
# Windows sends VC=0 on session setup after a reboot. Without this, smbd
# keeps the prior session from the same client IP alive and refuses the
# new one. This is the specific fix for "net use fails after VM reboot".
reset on zero vc = yes
lock directory = {{ .Dir }}/locks
state directory = {{ .Dir }}/state
cache directory = {{ .Dir }}/cache
pid directory = {{ .Dir }}/pid
ncalrpc dir = {{ .Dir }}/ncalrpc
private dir = {{ .Dir }}/private
usershare path = {{ .Dir }}/usershares
acl allow execute always = yes

{{- range $name, $path := .Shares }}

[{{ $name }}]
path = {{ $path }}
read only = yes
guest ok = yes
# Guest maps to "nobody" (uid 65534), which NFS-backed data dirs commonly
# reject (root squash / export rules only trust specific UIDs). The
# bootimus process already reads these files as root, so let smbd do the
# same. Shares are read-only, so this only widens reads.
force user = root
browseable = yes

{{- end }}
`))

	var buf bytes.Buffer
	err := configTemplate.Execute(&buf, &struct {
		Dir        string
		Interfaces string
		Port       int
		Shares     map[string]string
	}{
		Dir:        m.smbDir(),
		Interfaces: m.interfaces,
		Port:       m.port,
		Shares:     m.shares,
	})
	if err != nil {
		return err
	}

	return os.WriteFile(m.configPath(), buf.Bytes(), 0644)
}
