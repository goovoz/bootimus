package server

import (
	"errors"
	"net/http"
	"strconv"

	"bootimus/internal/diskimage"
	"bootimus/internal/models"
	"bootimus/internal/webhook"
)

func (s *Server) fireDiskImagingEvent(event string, job *models.DiskJob, image *models.DiskImage) {
	ev := webhook.Event{
		Event:      event,
		MAC:        job.ClientMAC,
		ClientName: job.ClientName,
		Metadata: map[string]string{
			"job_id": strconv.FormatUint(uint64(job.ID), 10),
			"kind":   job.Kind,
			"disk":   job.Disk,
			"state":  job.State,
		},
	}
	if image != nil {
		ev.Image = image.Name
		ev.Metadata["size_bytes"] = strconv.FormatInt(image.SizeBytes, 10)
	}
	if job.Message != "" {
		ev.Metadata["message"] = job.Message
	}
	s.webhookNotifier.Fire(ev)
}

func (s *Server) handleDiskImageJob(w http.ResponseWriter, r *http.Request) {
	if s.diskImages == nil {
		http.Error(w, "Disk imaging unavailable", http.StatusServiceUnavailable)
		return
	}
	script, err := s.diskImages.JobScript(r.URL.Query().Get("token"))
	if err != nil {
		s.logAndBroadcast("Disk imaging: job script refused for %s: %v", r.RemoteAddr, err)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript")
	w.Write([]byte(script))
}

func (s *Server) handleDiskImageCallback(w http.ResponseWriter, r *http.Request) {
	if s.diskImages == nil {
		http.Error(w, "Disk imaging unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	err := s.diskImages.Report(q.Get("token"), q.Get("status"), q.Get("disk"), q.Get("message"))
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	case errors.Is(err, diskimage.ErrUnknownToken), errors.Is(err, diskimage.ErrJobNotActive):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}
