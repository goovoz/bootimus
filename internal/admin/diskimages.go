package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"bootimus/internal/diskimage"
	"bootimus/internal/models"
	"bootimus/internal/storage"
	"bootimus/internal/wol"
)

func (h *Handler) diskImagingAvailable(w http.ResponseWriter) bool {
	if h.DiskImaging == nil {
		h.sendJSON(w, http.StatusServiceUnavailable, Response{Success: false, Error: "Disk imaging requires a database"})
		return false
	}
	return true
}

func (h *Handler) DiskImages(w http.ResponseWriter, r *http.Request) {
	if !h.diskImagingAvailable(w) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		images, err := h.DiskImaging.ListImages()
		if err != nil {
			h.sendJSON(w, http.StatusInternalServerError, Response{Success: false, Error: err.Error()})
			return
		}
		if images == nil {
			images = []*models.DiskImage{}
		}
		h.sendJSON(w, http.StatusOK, Response{Success: true, Data: map[string]interface{}{
			"status": h.DiskImaging.Status(),
			"images": images,
		}})
	case http.MethodPut:
		id, ok := parseIDParam(w, r, h)
		if !ok {
			return
		}
		var body struct {
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Invalid body"})
			return
		}
		image, err := h.DiskImaging.UpdateDescription(id, body.Description)
		if err != nil {
			h.sendJSON(w, http.StatusNotFound, Response{Success: false, Error: "Disk image not found"})
			return
		}
		h.sendJSON(w, http.StatusOK, Response{Success: true, Message: "Disk image updated", Data: image})
	case http.MethodDelete:
		id, ok := parseIDParam(w, r, h)
		if !ok {
			return
		}
		if err := h.DiskImaging.DeleteImage(id); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, storage.ErrDiskImageInUse) {
				status = http.StatusConflict
			}
			h.sendJSON(w, status, Response{Success: false, Error: err.Error()})
			return
		}
		h.sendJSON(w, http.StatusOK, Response{Success: true, Message: "Disk image deleted"})
	default:
		h.sendJSON(w, http.StatusMethodNotAllowed, Response{Success: false, Error: "Method not allowed"})
	}
}

func parseIDParam(w http.ResponseWriter, r *http.Request, h *Handler) (uint, bool) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 32)
	if err != nil || id == 0 {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Invalid id"})
		return 0, false
	}
	return uint(id), true
}

func diskImagingStatus(err error) int {
	switch {
	case errors.Is(err, diskimage.ErrClientUnknown):
		return http.StatusNotFound
	case errors.Is(err, diskimage.ErrJobActive), errors.Is(err, diskimage.ErrNameTaken):
		return http.StatusConflict
	case errors.Is(err, diskimage.ErrNotReady):
		return http.StatusPreconditionFailed
	case errors.Is(err, diskimage.ErrInvalidName), errors.Is(err, diskimage.ErrInvalidDisk), errors.Is(err, diskimage.ErrImageNotReady):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func (h *Handler) CaptureDisk(w http.ResponseWriter, r *http.Request) {
	if !h.diskImagingAvailable(w) {
		return
	}
	if r.Method != http.MethodPost {
		h.sendJSON(w, http.StatusMethodNotAllowed, Response{Success: false, Error: "Method not allowed"})
		return
	}
	var body struct {
		MACAddress  string `json:"mac_address"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Disk        string `json:"disk"`
		Wake        bool   `json:"wake"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Invalid body"})
		return
	}
	if body.MACAddress == "" {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Missing mac_address"})
		return
	}
	job, err := h.DiskImaging.Capture(diskimage.CaptureRequest{
		MAC:         body.MACAddress,
		Name:        body.Name,
		Description: body.Description,
		Disk:        body.Disk,
	})
	if err != nil {
		h.sendJSON(w, diskImagingStatus(err), Response{Success: false, Error: err.Error()})
		return
	}
	message := fmt.Sprintf("Capture of %s queued for the next PXE boot of %s", body.Name, job.ClientMAC)
	if body.Wake {
		message += h.wakeForJob(job.ClientMAC)
	}
	log.Printf("Admin: %s", message)
	h.sendJSON(w, http.StatusOK, Response{Success: true, Message: message, Data: job})
}

func (h *Handler) DeployDiskImage(w http.ResponseWriter, r *http.Request) {
	if !h.diskImagingAvailable(w) {
		return
	}
	if r.Method != http.MethodPost {
		h.sendJSON(w, http.StatusMethodNotAllowed, Response{Success: false, Error: "Method not allowed"})
		return
	}
	var body struct {
		MACAddress    string `json:"mac_address"`
		ClientGroupID uint   `json:"client_group_id"`
		ImageID       uint   `json:"image_id"`
		Disk          string `json:"disk"`
		Wake          bool   `json:"wake"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Invalid body"})
		return
	}
	if body.ImageID == 0 {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Missing image_id"})
		return
	}
	if body.MACAddress == "" && body.ClientGroupID == 0 {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Provide mac_address or client_group_id"})
		return
	}

	if body.MACAddress != "" {
		job, err := h.DiskImaging.Deploy(diskimage.DeployRequest{MAC: body.MACAddress, ImageID: body.ImageID, Disk: body.Disk})
		if err != nil {
			h.sendJSON(w, diskImagingStatus(err), Response{Success: false, Error: err.Error()})
			return
		}
		message := fmt.Sprintf("Deploy of %s queued for the next PXE boot of %s", job.DiskImage.Name, job.ClientMAC)
		if body.Wake {
			message += h.wakeForJob(job.ClientMAC)
		}
		log.Printf("Admin: %s", message)
		h.sendJSON(w, http.StatusOK, Response{Success: true, Message: message, Data: []*models.DiskJob{job}})
		return
	}

	group, err := h.storage.GetClientGroup(body.ClientGroupID)
	if err != nil {
		h.sendJSON(w, http.StatusNotFound, Response{Success: false, Error: "Group not found"})
		return
	}
	members, err := h.storage.ListClientsInGroup(group.ID)
	if err != nil {
		h.sendJSON(w, http.StatusInternalServerError, Response{Success: false, Error: err.Error()})
		return
	}
	var jobs []*models.DiskJob
	var failures []string
	for _, c := range members {
		if !c.Enabled {
			continue
		}
		job, err := h.DiskImaging.Deploy(diskimage.DeployRequest{MAC: c.MACAddress, ImageID: body.ImageID, Disk: body.Disk})
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", c.MACAddress, err))
			continue
		}
		jobs = append(jobs, job)
	}
	if jobs == nil {
		jobs = []*models.DiskJob{}
	}
	if body.Wake && len(jobs) > 0 {
		broadcast := h.wolBroadcastAddr
		if group.WOLBroadcastAddr != "" {
			broadcast = group.WOLBroadcastAddr
		}
		macs := make([]string, 0, len(jobs))
		for _, j := range jobs {
			macs = append(macs, j.ClientMAC)
		}
		go wakeStaggered(macs, broadcast, time.Duration(group.StaggerDelayMillis)*time.Millisecond)
	}
	message := fmt.Sprintf("Deploy queued for %d member(s) of %s", len(jobs), group.Name)
	if len(failures) > 0 {
		message += fmt.Sprintf("; %d skipped", len(failures))
	}
	log.Printf("Admin: %s", message)
	h.sendJSON(w, http.StatusOK, Response{
		Success: len(jobs) > 0,
		Message: message,
		Error:   joinFailures(failures),
		Data:    map[string]interface{}{"jobs": jobs, "failures": failures},
	})
}

func joinFailures(failures []string) string {
	if len(failures) == 0 {
		return ""
	}
	out := failures[0]
	for _, f := range failures[1:] {
		out += "; " + f
	}
	return out
}

func (h *Handler) wakeForJob(mac string) string {
	broadcast := h.wolBroadcastAddr
	if client, err := h.storage.GetClient(mac); err == nil && client.ClientGroupID != nil {
		if group, err := h.storage.GetClientGroup(*client.ClientGroupID); err == nil && group.WOLBroadcastAddr != "" {
			broadcast = group.WOLBroadcastAddr
		}
	}
	if err := wol.SendMagicPacket(mac, broadcast); err != nil {
		return fmt.Sprintf("; Wake-on-LAN failed: %v", err)
	}
	return "; Wake-on-LAN sent"
}

func wakeStaggered(macs []string, broadcast string, gap time.Duration) {
	for i, mac := range macs {
		if i > 0 && gap > 0 {
			time.Sleep(gap)
		}
		if err := wol.SendMagicPacket(mac, broadcast); err != nil {
			log.Printf("Disk imaging wake: failed for %s: %v", mac, err)
		}
	}
}

func (h *Handler) DiskJobs(w http.ResponseWriter, r *http.Request) {
	if !h.diskImagingAvailable(w) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 1000 {
				limit = n
			}
		}
		jobs, err := h.DiskImaging.ListJobs(r.URL.Query().Get("mac"), limit)
		if err != nil {
			h.sendJSON(w, http.StatusInternalServerError, Response{Success: false, Error: err.Error()})
			return
		}
		if jobs == nil {
			jobs = []*models.DiskJob{}
		}
		h.sendJSON(w, http.StatusOK, Response{Success: true, Data: jobs})
	case http.MethodDelete:
		id, ok := parseIDParam(w, r, h)
		if !ok {
			return
		}
		if err := h.DiskImaging.Cancel(id); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, diskimage.ErrJobNotActive) {
				status = http.StatusConflict
			}
			h.sendJSON(w, status, Response{Success: false, Error: err.Error()})
			return
		}
		h.sendJSON(w, http.StatusOK, Response{Success: true, Message: "Job cancelled"})
	default:
		h.sendJSON(w, http.StatusMethodNotAllowed, Response{Success: false, Error: "Method not allowed"})
	}
}
