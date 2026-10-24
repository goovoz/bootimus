package admin

// [bootimus-fork L1] fork-patch: download-resume / download-proxy 的进度管理回归测试。
//
// 这里的重点是「Get 必须给快照」：以前它返回内部指针，接口层在 json.Marshal 时
// 与下载协程的 Update 同时读写同一结构体，-race 下是明确的 DATA RACE。

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func newTestDownloadManager() *DownloadManager {
	return &DownloadManager{downloads: make(map[string]*DownloadProgress)}
}

func TestDownloadManagerGetReturnsSnapshot(t *testing.T) {
	dm := newTestDownloadManager()
	dm.TryBegin("http://example.invalid/a.iso", "a.iso")
	dm.Add("http://example.invalid/a.iso", "a.iso", 1024)
	dm.Update("a.iso", 512)

	snapshot := dm.Get("a.iso")
	if snapshot == nil {
		t.Fatal("Get returned nil")
	}
	snapshot.DownloadedBytes = 999
	snapshot.Status = "tampered"

	if internal := dm.Get("a.iso"); internal.DownloadedBytes != 512 || internal.Status != "downloading" {
		t.Fatalf("Get 泄漏了内部指针：%+v", internal)
	}

	all := dm.GetAll()
	if len(all) != 1 {
		t.Fatalf("GetAll = %d entries", len(all))
	}
	all[0].Status = "tampered"
	if internal := dm.Get("a.iso"); internal.Status != "downloading" {
		t.Fatal("GetAll 泄漏了内部指针")
	}
}

// 这条用例本身要靠 -race 才有意义：读侧 json.Marshal 与写侧 Update 并发。
func TestDownloadManagerConcurrentReadWhileUpdating(t *testing.T) {
	dm := newTestDownloadManager()
	dm.TryBegin("http://example.invalid/b.iso", "b.iso")
	dm.Add("http://example.invalid/b.iso", "b.iso", 1<<30)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20000; i++ {
			dm.Update("b.iso", int64(i*1024))
		}
		close(stop)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if p := dm.Get("b.iso"); p != nil {
				if _, err := json.Marshal(p); err != nil {
					t.Error(err)
					return
				}
			}
			for _, p := range dm.GetAll() {
				if _, err := json.Marshal(p); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}()

	wg.Wait()
}

// 续传时速度必须按「本次新传的字节」算：以前用累计字节 / 总耗时，
// 一开始就显示几个 G/s。
func TestDownloadManagerResumeSpeedUsesDelta(t *testing.T) {
	dm := newTestDownloadManager()
	dm.TryBegin("http://example.invalid/c.iso", "c.iso")
	dm.Add("http://example.invalid/c.iso", "c.iso", 1<<30)

	const resumeAt = 1 << 29 // 已经从 512MB 处续传
	dm.Update("c.iso", resumeAt)
	dm.Update("c.iso", resumeAt+1<<20)

	got := dm.Get("c.iso")
	if got.Speed == "" {
		t.Fatal("speed 未计算")
	}
	// 增量只有 1MB，不可能算出 GB/s 级别的速度
	if got.Speed != "" && got.Speed[0] > '9' {
		t.Fatalf("续传后的速度异常：%s", got.Speed)
	}
	if got.Percentage < 49 || got.Percentage > 51 {
		t.Fatalf("percentage = %.2f，应按磁盘上的总字节算", got.Percentage)
	}
}

func TestDownloadManagerPrunesFinishedEntries(t *testing.T) {
	dm := newTestDownloadManager()
	dm.TryBegin("http://example.invalid/d.iso", "d.iso")
	dm.Complete("d.iso")

	if n := dm.Prune(time.Hour); n != 0 {
		t.Fatalf("刚完成的条目不该被清理，实际删了 %d", n)
	}
	if p := dm.Get("d.iso"); p == nil {
		t.Fatal("条目不该消失")
	}

	// 把完成时间往前挪两小时
	dm.mu.Lock()
	dm.downloads["d.iso"].FinishedAt = time.Now().Add(-2 * time.Hour)
	dm.mu.Unlock()

	if n := dm.Prune(time.Hour); n != 1 {
		t.Fatalf("过期条目应被清理，实际 %d", n)
	}
	if p := dm.Get("d.iso"); p != nil {
		t.Fatal("过期条目仍在")
	}
}

func TestDownloadManagerPruneKeepsRunningDownloads(t *testing.T) {
	dm := newTestDownloadManager()
	dm.TryBegin("http://example.invalid/e.iso", "e.iso")
	dm.Add("http://example.invalid/e.iso", "e.iso", 1024)

	dm.mu.Lock()
	dm.downloads["e.iso"].FinishedAt = time.Now().Add(-48 * time.Hour)
	dm.mu.Unlock()

	if n := dm.Prune(time.Hour); n != 0 {
		t.Fatalf("正在下载的条目不能被清理，实际删了 %d", n)
	}
}

// TryBegin -> Add 之间不能重置 StartTime，否则「已用时间」会被清零。
func TestDownloadManagerAddKeepsStartTime(t *testing.T) {
	dm := newTestDownloadManager()
	dm.TryBegin("http://example.invalid/f.iso", "f.iso")
	first := dm.Get("f.iso").StartTime

	time.Sleep(5 * time.Millisecond)
	dm.Add("http://example.invalid/f.iso", "f.iso", 4096)

	if got := dm.Get("f.iso").StartTime; !got.Equal(first) {
		t.Fatalf("Add 重置了 StartTime：%v -> %v", first, got)
	}
	if got := dm.Get("f.iso").TotalBytes; got != 4096 {
		t.Fatalf("Add 没有更新 TotalBytes：%d", got)
	}
}
