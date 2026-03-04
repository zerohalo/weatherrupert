package announcements

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTmpCSV(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "announcements.csv")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadBasic(t *testing.T) {
	path := writeTmpCSV(t, "Hello World\nSecond announcement\n")
	anns := Load(path)
	if len(anns) != 2 {
		t.Fatalf("got %d announcements, want 2", len(anns))
	}
	if anns[0].Text != "Hello World" {
		t.Errorf("anns[0].Text = %q, want %q", anns[0].Text, "Hello World")
	}
	if anns[0].Date != "" {
		t.Errorf("anns[0].Date = %q, want empty", anns[0].Date)
	}
}

func TestLoadWithDates(t *testing.T) {
	path := writeTmpCSV(t, "Happy Valentine's Day!,02-14\nHappy New Year!,01-01\nEvery day announcement\n")
	anns := Load(path)
	if len(anns) != 3 {
		t.Fatalf("got %d announcements, want 3", len(anns))
	}
	if anns[0].Date != "02-14" {
		t.Errorf("anns[0].Date = %q, want %q", anns[0].Date, "02-14")
	}
	if anns[1].Date != "01-01" {
		t.Errorf("anns[1].Date = %q, want %q", anns[1].Date, "01-01")
	}
	if anns[2].Date != "" {
		t.Errorf("anns[2].Date = %q, want empty", anns[2].Date)
	}
}

func TestLoadWithHeader(t *testing.T) {
	path := writeTmpCSV(t, "text,date\nActual announcement\n")
	anns := Load(path)
	if len(anns) != 1 {
		t.Fatalf("got %d announcements, want 1 (header should be skipped)", len(anns))
	}
	if anns[0].Text != "Actual announcement" {
		t.Errorf("anns[0].Text = %q, want %q", anns[0].Text, "Actual announcement")
	}
}

func TestLoadSkipsEmptyRows(t *testing.T) {
	path := writeTmpCSV(t, "First\n\n  \nSecond\n")
	anns := Load(path)
	if len(anns) != 2 {
		t.Fatalf("got %d announcements, want 2", len(anns))
	}
}

func TestLoadInvalidDateIgnored(t *testing.T) {
	path := writeTmpCSV(t, "Announcement,not-a-date\n")
	anns := Load(path)
	if len(anns) != 1 {
		t.Fatalf("got %d announcements, want 1", len(anns))
	}
	if anns[0].Date != "" {
		t.Errorf("invalid date should be ignored, got %q", anns[0].Date)
	}
}

func TestLoadCommentLines(t *testing.T) {
	path := writeTmpCSV(t, "# This is a comment\nActual announcement\n")
	anns := Load(path)
	if len(anns) != 1 {
		t.Fatalf("got %d announcements, want 1 (comment should be skipped)", len(anns))
	}
}

func TestLoadNonexistentReturnsDefaults(t *testing.T) {
	anns := Load("/nonexistent/file.csv")
	if len(anns) == 0 {
		t.Fatal("expected defaults, got empty")
	}
	if anns[0].Text == "" {
		t.Error("default announcement should have text")
	}
}

func TestLoadEmptyFileReturnsDefaults(t *testing.T) {
	path := writeTmpCSV(t, "")
	anns := Load(path)
	if len(anns) == 0 {
		t.Fatal("empty file should return defaults")
	}
}

func TestLoadHeaderOnlyReturnsDefaults(t *testing.T) {
	path := writeTmpCSV(t, "text,date\n")
	anns := Load(path)
	if len(anns) == 0 {
		t.Fatal("header-only file should return defaults")
	}
}
