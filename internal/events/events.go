// Package events là bus sự kiện trong tiến trình, phục vụ luồng SSE đẩy về giao diện.
package events

import (
	"encoding/json"
	"log/slog"
	"sync"
)

// Loại sự kiện đẩy về giao diện.
const (
	KindJobProgress  = "job_progress"
	KindDomainStaged = "domain_staged"
	KindPublishDone  = "publish_done"
	KindSourceSynced = "source_synced"
	KindHealth       = "health"
)

// Event là một thông điệp đẩy về client.
type Event struct {
	Kind string          `json:"-"`
	Data json.RawMessage `json:"data"`
}

// Broker phát sự kiện tới các thuê bao đang mở.
type Broker struct {
	mu          sync.RWMutex
	subscribers map[chan Event]struct{}
	log         *slog.Logger
}

// NewBroker dựng broker.
func NewBroker(log *slog.Logger) *Broker {
	return &Broker{subscribers: make(map[chan Event]struct{}), log: log}
}

// Subscribe mở một kênh nhận sự kiện. Hàm trả về phải được gọi để dọn dẹp.
func (b *Broker) Subscribe() (<-chan Event, func()) {
	// Kênh có đệm: một client chậm không được chặn cả bus.
	ch := make(chan Event, 32)

	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subscribers[ch]; ok {
			delete(b.subscribers, ch)
			close(ch)
		}
	}
}

// Publish gửi một sự kiện tới mọi thuê bao.
//
// Không bao giờ chặn: sự kiện là thông tin phụ trợ cho giao diện, và một trình duyệt
// bị treo không được phép làm chậm pipeline xử lý.
func (b *Broker) Publish(kind string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		b.log.Warn("không mã hóa được sự kiện", "kind", kind, "err", err)
		return
	}
	ev := Event{Kind: kind, Data: data}

	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers {
		select {
		case ch <- ev:
		default: // thuê bao chậm: bỏ qua sự kiện này
		}
	}
}

// Count trả về số thuê bao đang mở.
func (b *Broker) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}
