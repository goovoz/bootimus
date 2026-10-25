package admin

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"bootimus/internal/models"
	"bootimus/internal/storage"
)

type sanTargetView struct {
	*models.SANTarget
	ClientCount int64 `json:"client_count"`
	GroupCount  int64 `json:"group_count"`
}

func (h *Handler) SANTargets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listSANTargets(w)
	case http.MethodPost:
		h.createSANTarget(w, r)
	case http.MethodPut:
		h.updateSANTarget(w, r)
	case http.MethodDelete:
		h.deleteSANTarget(w, r)
	default:
		h.sendJSON(w, http.StatusMethodNotAllowed, Response{Success: false, Error: "Method not allowed"})
	}
}

func (h *Handler) listSANTargets(w http.ResponseWriter) {
	targets, err := h.storage.ListSANTargets()
	if err != nil {
		h.sendJSON(w, http.StatusInternalServerError, Response{Success: false, Error: err.Error()})
		return
	}
	views := make([]sanTargetView, 0, len(targets))
	for _, t := range targets {
		clients, groups, countErr := h.storage.CountSANTargetUsers(t.ID)
		if countErr != nil {
			log.Printf("SAN: failed to count users of target %d: %v", t.ID, countErr)
		}
		views = append(views, sanTargetView{SANTarget: t, ClientCount: clients, GroupCount: groups})
	}
	h.sendJSON(w, http.StatusOK, Response{Success: true, Data: views})
}

func (h *Handler) createSANTarget(w http.ResponseWriter, r *http.Request) {
	var target models.SANTarget
	if err := json.NewDecoder(r.Body).Decode(&target); err != nil {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Invalid request body"})
		return
	}
	target.ID = 0
	if err := h.storage.CreateSANTarget(&target); err != nil {
		h.sendJSON(w, sanErrorStatus(err), Response{Success: false, Error: err.Error()})
		return
	}
	log.Printf("Admin: SAN target created - %s (%s)", target.Name, target.URI)
	h.sendJSON(w, http.StatusCreated, Response{Success: true, Message: "SAN target created", Data: target})
}

func (h *Handler) updateSANTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := h.sanTargetID(w, r)
	if !ok {
		return
	}
	if _, err := h.storage.GetSANTarget(id); err != nil {
		h.sendJSON(w, http.StatusNotFound, Response{Success: false, Error: "SAN target not found"})
		return
	}
	var target models.SANTarget
	if err := json.NewDecoder(r.Body).Decode(&target); err != nil {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Invalid request body"})
		return
	}
	if err := h.storage.UpdateSANTarget(id, &target); err != nil {
		h.sendJSON(w, sanErrorStatus(err), Response{Success: false, Error: err.Error()})
		return
	}
	log.Printf("Admin: SAN target updated - %s (%s)", target.Name, target.URI)
	h.sendJSON(w, http.StatusOK, Response{Success: true, Message: "SAN target updated", Data: target})
}

func (h *Handler) deleteSANTarget(w http.ResponseWriter, r *http.Request) {
	id, ok := h.sanTargetID(w, r)
	if !ok {
		return
	}
	target, err := h.storage.GetSANTarget(id)
	if err != nil {
		h.sendJSON(w, http.StatusNotFound, Response{Success: false, Error: "SAN target not found"})
		return
	}
	if err := h.storage.DeleteSANTarget(id); err != nil {
		h.sendJSON(w, http.StatusInternalServerError, Response{Success: false, Error: err.Error()})
		return
	}
	log.Printf("Admin: SAN target deleted - %s", target.Name)
	h.sendJSON(w, http.StatusOK, Response{Success: true, Message: "SAN target deleted"})
}

func (h *Handler) sanTargetID(w http.ResponseWriter, r *http.Request) (uint, bool) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 32)
	if err != nil || id == 0 {
		h.sendJSON(w, http.StatusBadRequest, Response{Success: false, Error: "Invalid SAN target ID"})
		return 0, false
	}
	return uint(id), true
}

func sanErrorStatus(err error) int {
	if errors.Is(err, storage.ErrSANTargetExists) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}
