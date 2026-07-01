package pc

import (
	"embed"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

//go:embed testdata/*.las
var testdataLas embed.FS

func writeEmbeddedLasFiles(t *testing.T) ([]string, string) {
	t.Helper()
	dir := t.TempDir()
	entries, err := testdataLas.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		data, err := testdataLas.ReadFile("testdata/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, e.Name())
		if err := os.WriteFile(p, data, 0644); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}
	return files, dir
}

func TestCombinedReader(t *testing.T) {
	files, _ := writeEmbeddedLasFiles(t)

	r, err := NewCombinedPointCloudReader(files, "EPSG:32633", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer r.Close()

	if actual := r.NumberOfPoints(); actual != 10*len(files) {
		t.Errorf("expected %d points got %d", 10*len(files), actual)
	}

	if actual := r.GetCRS(); actual != "EPSG:32633" {
		t.Errorf("expected epsg %d got epsg %s", 32633, actual)
	}

	for i := 0; i < r.NumberOfPoints(); i++ {
		_, err := r.GetNext()
		if err != nil {
			t.Errorf("unexpected error %v", err)
		}
	}
	_, err = r.GetNext()
	if err == nil {
		t.Errorf("expected error, got none")
	}
}

func TestCombinedReaderConcurrency(t *testing.T) {
	files, _ := writeEmbeddedLasFiles(t)

	r, err := NewCombinedPointCloudReader(files, "EPSG:32633", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer r.Close()

	if actual := r.NumberOfPoints(); actual != 10*len(files) {
		t.Errorf("expected %d points got %d", 10*len(files), actual)
	}

	if actual := r.GetCRS(); actual != "EPSG:32633" {
		t.Errorf("expected epsg %d got epsg %s", 32633, actual)
	}

	e := make(chan error, 10)
	readFun := func(wg *sync.WaitGroup) {
		defer wg.Done()
		read := 0
		for i := 0; i < r.NumberOfPoints()/5; i++ {
			_, err := r.GetNext()
			if err != nil {
				e <- err
				t.Errorf("unexpected error %v", err)
				continue
			}
			read++
		}
	}
	wg := &sync.WaitGroup{}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go readFun(wg)
	}
	wg.Wait()
	if len(e) > 0 {
		t.Errorf("errors detected in the error channel but none expected")
	}
}
