package diskimage

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"bootimus/internal/models"
	"bootimus/internal/tools"

	"gorm.io/gorm"
)

const (
	DirName    = "disk-images"
	StagingDir = ".staging"
	ToolName   = "clonezilla"
	ExportName = "disk-images"

	EventCaptureCompleted = "disk.capture_completed"
	EventCaptureFailed    = "disk.capture_failed"
	EventDeployCompleted  = "disk.deploy_completed"
	EventDeployFailed     = "disk.deploy_failed"

	DefaultDisk = "sda"
	AutoDisk    = "auto"
)

var (
	ErrNotReady        = errors.New("disk imaging is not ready")
	ErrJobActive       = errors.New("client already has an active disk imaging job")
	ErrImageNotReady   = errors.New("disk image is not ready to deploy")
	ErrJobNotActive    = errors.New("job is no longer active")
	ErrUnknownToken    = errors.New("unknown job token")
	ErrInvalidName     = errors.New("image name must be 1-64 characters of lower-case letters, digits, dots, dashes or underscores and start with a letter or digit")
	ErrInvalidDisk     = errors.New("disk must be a device name such as sda or nvme0n1, or auto")
	ErrNameTaken       = errors.New("a disk image with that name already exists")
	ErrClientUnknown   = errors.New("client not found")
	ErrToolUnavailable = errors.New("the Clonezilla tool is not downloaded and enabled")
	ErrNFSDisabled     = errors.New("the NFS server is disabled; start Bootimus with --nfs-enabled")
)

var (
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	diskPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)
)

type Store interface {
	GetClient(mac string) (*models.Client, error)
	CreateDiskImage(image *models.DiskImage) error
	GetDiskImage(id uint) (*models.DiskImage, error)
	GetDiskImageByName(name string) (*models.DiskImage, error)
	ListDiskImages() ([]*models.DiskImage, error)
	UpdateDiskImage(image *models.DiskImage) error
	DeleteDiskImage(id uint) error
	CreateDiskJob(job *models.DiskJob) error
	GetDiskJob(id uint) (*models.DiskJob, error)
	GetDiskJobByToken(token string) (*models.DiskJob, error)
	ListDiskJobs(mac string, limit int) ([]*models.DiskJob, error)
	ActiveDiskJobForClient(mac string) (*models.DiskJob, error)
	UpdateDiskJob(job *models.DiskJob) error
}

type ToolSource interface {
	GetEnabledTools(serverURL string) []tools.EnabledTool
}

type Config struct {
	DataDir    string
	ServerAddr string
	HTTPPort   int
	NFSPort    int
	NFSEnabled bool
}

type Status struct {
	NFSEnabled bool     `json:"nfs_enabled"`
	ToolReady  bool     `json:"tool_ready"`
	Ready      bool     `json:"ready"`
	Problems   []string `json:"problems"`
}

type Boot struct {
	KernelURL string
	InitrdURL string
	Params    string
	Label     string
}

type CaptureRequest struct {
	MAC         string
	Name        string
	Description string
	Disk        string
}

type DeployRequest struct {
	MAC     string
	ImageID uint
	Disk    string
}

type Manager struct {
	store   Store
	cfg     Config
	tools   ToolSource
	OnEvent func(event string, job *models.DiskJob, image *models.DiskImage)
	mu      sync.Mutex
}

func New(store Store, cfg Config, tools ToolSource) *Manager {
	return &Manager{store: store, cfg: cfg, tools: tools}
}

func (m *Manager) Root() string {
	return filepath.Join(m.cfg.DataDir, DirName)
}

func (m *Manager) imagePath(name string) string {
	return filepath.Join(m.Root(), name)
}

func (m *Manager) stagingPath(token string) string {
	return filepath.Join(m.Root(), StagingDir, token)
}

func (m *Manager) serverURL() string {
	return fmt.Sprintf("http://%s:%d", m.cfg.ServerAddr, m.cfg.HTTPPort)
}

func (m *Manager) clonezilla() (*tools.EnabledTool, bool) {
	if m.tools == nil {
		return nil, false
	}
	return findClonezilla(m.tools.GetEnabledTools(m.serverURL()))
}

func findClonezilla(enabled []tools.EnabledTool) (*tools.EnabledTool, bool) {
	for i := range enabled {
		if enabled[i].Name == ToolName && enabled[i].KernelURL != "" && enabled[i].InitrdURL != "" {
			return &enabled[i], true
		}
	}
	return nil, false
}

func (m *Manager) Status() Status {
	st := Status{NFSEnabled: m.cfg.NFSEnabled, Problems: []string{}}
	if !st.NFSEnabled {
		st.Problems = append(st.Problems, ErrNFSDisabled.Error())
	}
	if _, ok := m.clonezilla(); ok {
		st.ToolReady = true
	} else {
		st.Problems = append(st.Problems, ErrToolUnavailable.Error())
	}
	st.Ready = st.NFSEnabled && st.ToolReady
	return st
}

func (m *Manager) ready() error {
	st := m.Status()
	if st.Ready {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrNotReady, strings.Join(st.Problems, "; "))
}

func ValidName(name string) error {
	if !namePattern.MatchString(name) {
		return ErrInvalidName
	}
	return nil
}

func NormaliseDisk(disk string) (string, error) {
	disk = strings.TrimSpace(strings.TrimPrefix(disk, "/dev/"))
	if disk == "" {
		return DefaultDisk, nil
	}
	if disk == AutoDisk {
		return AutoDisk, nil
	}
	if !diskPattern.MatchString(disk) {
		return "", ErrInvalidDisk
	}
	return disk, nil
}

func normaliseMAC(mac string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(mac), "-", ":"))
}

func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (m *Manager) Capture(req CaptureRequest) (*models.DiskJob, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	if err := ValidName(req.Name); err != nil {
		return nil, err
	}
	disk, err := NormaliseDisk(req.Disk)
	if err != nil {
		return nil, err
	}
	mac := normaliseMAC(req.MAC)
	client, err := m.store.GetClient(mac)
	if err != nil {
		return nil, ErrClientUnknown
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, err := m.store.ActiveDiskJobForClient(mac); err == nil && existing != nil {
		return nil, ErrJobActive
	}
	if _, err := m.store.GetDiskImageByName(req.Name); err == nil {
		return nil, ErrNameTaken
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	image := &models.DiskImage{
		Name:             req.Name,
		Description:      req.Description,
		SourceMAC:        mac,
		SourceClientName: client.Name,
		Disk:             disk,
		State:            models.DiskImageStateCapturing,
	}
	if err := m.store.CreateDiskImage(image); err != nil {
		return nil, err
	}
	job := &models.DiskJob{
		ClientMAC:   mac,
		ClientName:  client.Name,
		Kind:        models.DiskJobKindCapture,
		DiskImageID: image.ID,
		Disk:        disk,
		State:       models.DiskJobStatePending,
		Token:       newToken(),
	}
	if err := os.MkdirAll(m.stagingPath(job.Token), 0o755); err != nil {
		return nil, fmt.Errorf("create capture staging directory: %w", err)
	}
	if err := m.store.CreateDiskJob(job); err != nil {
		os.RemoveAll(m.stagingPath(job.Token))
		return nil, err
	}
	job.DiskImage = image
	log.Printf("Disk imaging: capture of %s from %s queued as job %d", image.Name, mac, job.ID)
	return job, nil
}

func (m *Manager) Deploy(req DeployRequest) (*models.DiskJob, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	disk, err := NormaliseDisk(req.Disk)
	if err != nil {
		return nil, err
	}
	mac := normaliseMAC(req.MAC)
	client, err := m.store.GetClient(mac)
	if err != nil {
		return nil, ErrClientUnknown
	}
	image, err := m.store.GetDiskImage(req.ImageID)
	if err != nil {
		return nil, fmt.Errorf("disk image not found")
	}
	if image.State != models.DiskImageStateReady {
		return nil, ErrImageNotReady
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, err := m.store.ActiveDiskJobForClient(mac); err == nil && existing != nil {
		return nil, ErrJobActive
	}
	job := &models.DiskJob{
		ClientMAC:   mac,
		ClientName:  client.Name,
		Kind:        models.DiskJobKindDeploy,
		DiskImageID: image.ID,
		Disk:        disk,
		State:       models.DiskJobStatePending,
		Token:       newToken(),
	}
	if err := m.store.CreateDiskJob(job); err != nil {
		return nil, err
	}
	job.DiskImage = image
	log.Printf("Disk imaging: deploy of %s to %s queued as job %d", image.Name, mac, job.ID)
	return job, nil
}

func (m *Manager) ActiveJob(mac string) *models.DiskJob {
	job, err := m.store.ActiveDiskJobForClient(normaliseMAC(mac))
	if err != nil {
		return nil
	}
	return job
}

func (m *Manager) Cancel(jobID uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, err := m.store.GetDiskJob(jobID)
	if err != nil {
		return err
	}
	if !job.Active() {
		return ErrJobNotActive
	}
	now := time.Now()
	job.State = models.DiskJobStateCancelled
	job.FinishedAt = &now
	if err := m.store.UpdateDiskJob(job); err != nil {
		return err
	}
	if job.Kind == models.DiskJobKindCapture {
		os.RemoveAll(m.stagingPath(job.Token))
		if job.DiskImage != nil && job.DiskImage.State == models.DiskImageStateCapturing {
			if err := m.store.DeleteDiskImage(job.DiskImageID); err != nil {
				log.Printf("Disk imaging: failed to remove unfinished image %d: %v", job.DiskImageID, err)
			}
		}
	}
	log.Printf("Disk imaging: job %d cancelled", job.ID)
	return nil
}

func (m *Manager) DeleteImage(id uint) error {
	image, err := m.store.GetDiskImage(id)
	if err != nil {
		return err
	}
	if err := m.store.DeleteDiskImage(id); err != nil {
		return err
	}
	if err := os.RemoveAll(m.imagePath(image.Name)); err != nil {
		return fmt.Errorf("remove image directory: %w", err)
	}
	log.Printf("Disk imaging: image %s deleted", image.Name)
	return nil
}

func (m *Manager) exportPath(job *models.DiskJob) string {
	if job.Kind == models.DiskJobKindCapture {
		return "/" + ExportName + "/" + StagingDir + "/" + job.Token
	}
	return "/" + ExportName
}

func (m *Manager) jobURL(token string) string {
	return fmt.Sprintf("%s/diskimage/job?token=%s", m.serverURL(), url.QueryEscape(token))
}

func (m *Manager) callbackURL(token string) string {
	return fmt.Sprintf("%s/diskimage/callback?token=%s", m.serverURL(), url.QueryEscape(token))
}

func (m *Manager) Boot(job *models.DiskJob, enabled []tools.EnabledTool) (*Boot, error) {
	tool, ok := findClonezilla(enabled)
	if !ok {
		return nil, ErrToolUnavailable
	}
	if job.DiskImage == nil {
		image, err := m.store.GetDiskImage(job.DiskImageID)
		if err != nil {
			return nil, err
		}
		job.DiskImage = image
	}
	params := strings.TrimSpace(tool.BootParams) + fmt.Sprintf(
		` ocs_prerun="wget -q -O /tmp/bootimus-job.sh %s" ocs_live_run="bash /tmp/bootimus-job.sh" ocs_live_batch="yes" ocs_live_extra_param="" keyboard-layouts=NONE locales=en_US.UTF-8 noprompt nosplash`,
		m.jobURL(job.Token))
	label := fmt.Sprintf("Capturing disk %s to image %s", job.Disk, job.DiskImage.Name)
	if job.Kind == models.DiskJobKindDeploy {
		label = fmt.Sprintf("Deploying image %s to disk %s", job.DiskImage.Name, job.Disk)
	}
	return &Boot{KernelURL: tool.KernelURL, InitrdURL: tool.InitrdURL, Params: params, Label: label}, nil
}

func (m *Manager) JobScript(token string) (string, error) {
	job, err := m.store.GetDiskJobByToken(token)
	if err != nil {
		return "", ErrUnknownToken
	}
	if !job.Active() {
		return "", ErrJobNotActive
	}
	if job.DiskImage == nil {
		return "", fmt.Errorf("job %d has no image", job.ID)
	}
	if job.State == models.DiskJobStatePending {
		now := time.Now()
		job.State = models.DiskJobStateRunning
		job.StartedAt = &now
		if err := m.store.UpdateDiskJob(job); err != nil {
			log.Printf("Disk imaging: failed to mark job %d running: %v", job.ID, err)
		}
		log.Printf("Disk imaging: job %d (%s %s) started on %s", job.ID, job.Kind, job.DiskImage.Name, job.ClientMAC)
	}
	return m.renderScript(job), nil
}

func (m *Manager) renderScript(job *models.DiskJob) string {
	var ocs, finish string
	if job.Kind == models.DiskJobKindCapture {
		ocs = fmt.Sprintf("ocs-sr -q2 -j2 -z1p -i 4096 -sc -p true savedisk %s \"$DISK\"", job.DiskImage.Name)
		finish = "poweroff"
	} else {
		ocs = fmt.Sprintf("ocs-sr -g auto -e1 auto -e2 -r -j2 -scr -p true restoredisk %s \"$DISK\"", job.DiskImage.Name)
		finish = "reboot"
	}
	var sb strings.Builder
	sb.WriteString("#!/bin/bash\n")
	sb.WriteString("export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\n")
	sb.WriteString(fmt.Sprintf("CALLBACK='%s'\n", m.callbackURL(job.Token)))
	sb.WriteString(fmt.Sprintf("DISK='%s'\n", job.Disk))
	sb.WriteString("report() {\n")
	sb.WriteString("  curl -fsS -m 20 \"${CALLBACK}&status=$1&disk=${DISK}&message=$2\" >/dev/null 2>&1 || wget -q -T 20 -O /dev/null \"${CALLBACK}&status=$1&disk=${DISK}&message=$2\"\n")
	sb.WriteString("}\n")
	sb.WriteString("fail() {\n")
	sb.WriteString("  echo \"Bootimus disk imaging failed: $1\"\n")
	sb.WriteString("  report failed \"$1\"\n")
	sb.WriteString("  umount /home/partimag 2>/dev/null\n")
	sb.WriteString("  sleep 30\n")
	sb.WriteString("  reboot\n")
	sb.WriteString("}\n")
	sb.WriteString("if [ \"$DISK\" = auto ]; then\n")
	sb.WriteString("  DISK=$(lsblk -dno NAME,TYPE,RM,RO | awk '$2==\"disk\" && $3==\"0\" && $4==\"0\" {print $1; exit}')\n")
	sb.WriteString("fi\n")
	sb.WriteString("[ -n \"$DISK\" ] || fail no_disk_found\n")
	sb.WriteString("mkdir -p /home/partimag\n")
	sb.WriteString(fmt.Sprintf("mount -t nfs -o nolock,vers=3,tcp,port=%d,mountport=%d %s:%s /home/partimag || fail nfs_mount_failed\n",
		m.cfg.NFSPort, m.cfg.NFSPort, m.cfg.ServerAddr, m.exportPath(job)))
	sb.WriteString("report running started\n")
	sb.WriteString(ocs + "\n")
	sb.WriteString("rc=$?\n")
	sb.WriteString("sync\n")
	sb.WriteString("umount /home/partimag 2>/dev/null\n")
	sb.WriteString("if [ $rc -ne 0 ]; then\n")
	sb.WriteString("  echo \"ocs-sr exited with status $rc\"\n")
	sb.WriteString("  report failed ocs-sr_exit_$rc\n")
	sb.WriteString("  sleep 30\n")
	sb.WriteString("  reboot\n")
	sb.WriteString("fi\n")
	sb.WriteString("report done ok\n")
	sb.WriteString("sleep 3\n")
	sb.WriteString(finish + "\n")
	return sb.String()
}

func (m *Manager) Report(token, status, disk, message string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, err := m.store.GetDiskJobByToken(token)
	if err != nil {
		return ErrUnknownToken
	}
	if !job.Active() {
		return ErrJobNotActive
	}
	if disk != "" && disk != AutoDisk {
		if d, err := NormaliseDisk(disk); err == nil {
			job.Disk = d
		}
	}
	now := time.Now()
	switch status {
	case "running":
		if job.State == models.DiskJobStatePending {
			job.State = models.DiskJobStateRunning
			job.StartedAt = &now
		}
		job.Message = message
		return m.store.UpdateDiskJob(job)
	case "done":
		job.State = models.DiskJobStateCompleted
		job.FinishedAt = &now
		job.Message = ""
		if job.Kind == models.DiskJobKindCapture {
			if err := m.finishCapture(job); err != nil {
				job.State = models.DiskJobStateFailed
				job.Message = err.Error()
				m.markImageFailed(job)
			}
		}
	case "failed":
		job.State = models.DiskJobStateFailed
		job.FinishedAt = &now
		job.Message = message
		if job.Kind == models.DiskJobKindCapture {
			os.RemoveAll(m.stagingPath(job.Token))
			m.markImageFailed(job)
		}
	default:
		return fmt.Errorf("unknown status %q", status)
	}
	if err := m.store.UpdateDiskJob(job); err != nil {
		return err
	}
	log.Printf("Disk imaging: job %d (%s) %s%s", job.ID, job.Kind, job.State, messageSuffix(job.Message))
	m.fire(job)
	return nil
}

func (m *Manager) markImageFailed(job *models.DiskJob) {
	if job.DiskImage == nil {
		return
	}
	job.DiskImage.State = models.DiskImageStateFailed
	if err := m.store.UpdateDiskImage(job.DiskImage); err != nil {
		log.Printf("Disk imaging: failed to mark image %d failed: %v", job.DiskImageID, err)
	}
}

func messageSuffix(msg string) string {
	if msg == "" {
		return ""
	}
	return ": " + msg
}

func (m *Manager) finishCapture(job *models.DiskJob) error {
	image := job.DiskImage
	if image == nil {
		return fmt.Errorf("image record missing")
	}
	src := filepath.Join(m.stagingPath(job.Token), image.Name)
	if _, err := os.Stat(src); err != nil {
		os.RemoveAll(m.stagingPath(job.Token))
		return fmt.Errorf("captured image directory %s not found in staging area", image.Name)
	}
	dest := m.imagePath(image.Name)
	os.RemoveAll(dest)
	if err := os.Rename(src, dest); err != nil {
		return fmt.Errorf("move captured image into place: %w", err)
	}
	os.RemoveAll(m.stagingPath(job.Token))
	image.SizeBytes = dirSize(dest)
	image.Disk = job.Disk
	image.State = models.DiskImageStateReady
	return m.store.UpdateDiskImage(image)
}

func dirSize(root string) int64 {
	var total int64
	filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func (m *Manager) fire(job *models.DiskJob) {
	if m.OnEvent == nil {
		return
	}
	var event string
	switch {
	case job.Kind == models.DiskJobKindCapture && job.State == models.DiskJobStateCompleted:
		event = EventCaptureCompleted
	case job.Kind == models.DiskJobKindCapture:
		event = EventCaptureFailed
	case job.State == models.DiskJobStateCompleted:
		event = EventDeployCompleted
	default:
		event = EventDeployFailed
	}
	m.OnEvent(event, job, job.DiskImage)
}

func (m *Manager) ListImages() ([]*models.DiskImage, error) {
	return m.store.ListDiskImages()
}

func (m *Manager) ListJobs(mac string, limit int) ([]*models.DiskJob, error) {
	return m.store.ListDiskJobs(normaliseMAC(mac), limit)
}

func (m *Manager) UpdateDescription(id uint, description string) (*models.DiskImage, error) {
	image, err := m.store.GetDiskImage(id)
	if err != nil {
		return nil, err
	}
	image.Description = description
	if err := m.store.UpdateDiskImage(image); err != nil {
		return nil, err
	}
	return image, nil
}
