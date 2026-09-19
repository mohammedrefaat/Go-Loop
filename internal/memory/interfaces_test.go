package memory

import (
	"context"
	"testing"
)

type mockMemoryStore struct {
	items map[string]*MemoryItem
}

func (m *mockMemoryStore) Save(ctx context.Context, item *MemoryItem) error {
	m.items[item.ID] = item
	return nil
}
func (m *mockMemoryStore) Get(ctx context.Context, id string) (*MemoryItem, error) {
	return m.items[id], nil
}
func (m *mockMemoryStore) Search(ctx context.Context, query MemoryQuery) ([]*MemoryItem, error) {
	var res []*MemoryItem
	for _, it := range m.items {
		res = append(res, it)
	}
	return res, nil
}
func (m *mockMemoryStore) Delete(ctx context.Context, id string) error {
	delete(m.items, id)
	return nil
}
func (m *mockMemoryStore) Close() error { return nil }

func TestMemoryStoreInterface(t *testing.T) {
	store := &mockMemoryStore{items: make(map[string]*MemoryItem)}
	var ms MemoryStore = store

	item := &MemoryItem{ID: "m1", Project: "p1", Title: "t1", Text: "text"}
	if err := ms.Save(context.Background(), item); err != nil {
		t.Fatalf("unexpected save error: %v", err)
	}

	got, err := ms.Get(context.Background(), "m1")
	if err != nil || got == nil || got.Title != "t1" {
		t.Fatalf("unexpected get result: %+v, %v", got, err)
	}
}
