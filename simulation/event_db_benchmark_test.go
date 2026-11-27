package simulation

import (
	"path/filepath"
	"testing"

	"smp/model"
)

func BenchmarkEventDBFlush(b *testing.B) {
	const batchSize = 1024
	db, err := OpenEventDB(filepath.Join(b.TempDir(), "events.db"), batchSize)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := db.Close(); err != nil {
			b.Error(err)
		}
	})
	events := make([]*model.EventRecord, batchSize)
	for i := range events {
		events[i] = &model.EventRecord{Type: "Post", AgentID: int64(i), Step: 1, Body: model.PostEventBody[float64]{
			Record: &model.PostRecord[float64]{AgentID: int64(i), Step: 1, Opinion: float64(i) / batchSize}, IsRepost: true,
		}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for _, event := range events {
			if err := db.StoreEvent(event); err != nil {
				b.Fatal(err)
			}
		}
	}
}
