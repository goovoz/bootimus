package storage

import (
	"testing"

	"bootimus/internal/models"
)

func TestDiskJobsListingAndActiveLookup(t *testing.T) {
	store := newTestStore(t)
	image := &models.DiskImage{Name: "gold", State: models.DiskImageStateReady}
	if err := store.CreateDiskImage(image); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDiskImage(&models.DiskImage{Name: "gold"}); err == nil {
		t.Fatal("duplicate image names must be rejected")
	}
	jobs := []*models.DiskJob{
		{ClientMAC: "aa:bb:cc:dd:ee:01", Kind: models.DiskJobKindDeploy, DiskImageID: image.ID, State: models.DiskJobStateCompleted, Token: "t1"},
		{ClientMAC: "aa:bb:cc:dd:ee:01", Kind: models.DiskJobKindDeploy, DiskImageID: image.ID, State: models.DiskJobStatePending, Token: "t2"},
		{ClientMAC: "aa:bb:cc:dd:ee:02", Kind: models.DiskJobKindCapture, DiskImageID: image.ID, State: models.DiskJobStateRunning, Token: "t3"},
	}
	for _, j := range jobs {
		if err := store.CreateDiskJob(j); err != nil {
			t.Fatal(err)
		}
	}
	all, err := store.ListDiskJobs("", 0)
	if err != nil || len(all) != 3 || all[0].Token != "t3" || all[0].DiskImage == nil {
		t.Fatalf("ListDiskJobs = %v, %v", all, err)
	}
	mine, _ := store.ListDiskJobs("aa:bb:cc:dd:ee:01", 1)
	if len(mine) != 1 || mine[0].Token != "t2" {
		t.Fatalf("filtered jobs = %+v", mine)
	}
	active, err := store.ActiveDiskJobForClient("aa:bb:cc:dd:ee:01")
	if err != nil || active.Token != "t2" {
		t.Fatalf("active = %+v, %v", active, err)
	}
	if _, err := store.ActiveDiskJobForClient("aa:bb:cc:dd:ee:03"); err == nil {
		t.Fatal("no active job expected for unknown client")
	}
	if err := store.DeleteDiskImage(image.ID); err != ErrDiskImageInUse {
		t.Fatalf("DeleteDiskImage err = %v, want ErrDiskImageInUse", err)
	}
	for _, j := range all {
		j.State = models.DiskJobStateCancelled
		if err := store.UpdateDiskJob(j); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteDiskImage(image.ID); err != nil {
		t.Fatal(err)
	}
	byToken, err := store.GetDiskJobByToken("t3")
	if err != nil || byToken.Kind != models.DiskJobKindCapture {
		t.Fatalf("GetDiskJobByToken = %+v, %v", byToken, err)
	}
}
