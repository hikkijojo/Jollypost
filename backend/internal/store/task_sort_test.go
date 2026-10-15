package store

import "testing"

func TestSortTasksByCodeDate(t *testing.T) {
	tasks := []Task{
		{ID: 4, Code: "10/15-2"},
		{ID: 1, Code: "10/11-12"},
		{ID: 2, Code: "10/12-1"},
		{ID: 3, Code: "10/11-3"},
		{ID: 5, Code: "10/15-1"},
	}
	sortTasksByCodeDate(tasks)
	got := codes(tasks)
	want := []string{"10/11-3", "10/11-12", "10/12-1", "10/15-1", "10/15-2"}
	if !same(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSortTasksByCodeDateAcrossMonthBoundary(t *testing.T) {
	tasks := []Task{
		{ID: 1, Code: "1/3-1"},
		{ID: 2, Code: "12/28-2"},
		{ID: 3, Code: "1/1-4"},
		{ID: 4, Code: "12/26-1"},
	}
	sortTasksByCodeDate(tasks)
	got := codes(tasks)
	want := []string{"12/26-1", "12/28-2", "1/1-4", "1/3-1"}
	if !same(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func codes(tasks []Task) []string {
	out := make([]string, len(tasks))
	for i, task := range tasks {
		out[i] = task.Code
	}
	return out
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
