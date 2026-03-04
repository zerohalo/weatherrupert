package trivia

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeTmpCSV(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trivia.csv")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadBasic(t *testing.T) {
	path := writeTmpCSV(t, "What color is the sky?,Blue\nWhat is 2+2?,4\n")
	items := Load(path)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Question != "What color is the sky?" {
		t.Errorf("items[0].Question = %q", items[0].Question)
	}
	if items[0].Answer != "Blue" {
		t.Errorf("items[0].Answer = %q", items[0].Answer)
	}
}

func TestLoadWithHeader(t *testing.T) {
	path := writeTmpCSV(t, "question,answer\nWhat is Go?,A language\n")
	items := Load(path)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (header should be skipped)", len(items))
	}
	if items[0].Question != "What is Go?" {
		t.Errorf("items[0].Question = %q", items[0].Question)
	}
}

func TestLoadSkipsIncompleteRows(t *testing.T) {
	path := writeTmpCSV(t, "Only question no answer\nValid question?,Valid answer\n")
	items := Load(path)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (single-column row should be skipped)", len(items))
	}
}

func TestLoadSkipsEmptyFields(t *testing.T) {
	path := writeTmpCSV(t, ",answer\nquestion,\nReal Q?,Real A\n")
	items := Load(path)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (empty question/answer rows should be skipped)", len(items))
	}
}

func TestLoadCommentLines(t *testing.T) {
	path := writeTmpCSV(t, "# comment line\nQ?,A\n")
	items := Load(path)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
}

func TestLoadNonexistentReturnsDefaults(t *testing.T) {
	items := Load("/nonexistent/file.csv")
	if len(items) == 0 {
		t.Fatal("expected defaults, got empty")
	}
	if items[0].Question == "" || items[0].Answer == "" {
		t.Error("default items should have question and answer")
	}
}

func TestLoadEmptyFileReturnsDefaults(t *testing.T) {
	path := writeTmpCSV(t, "")
	items := Load(path)
	if len(items) == 0 {
		t.Fatal("empty file should return defaults")
	}
}

func TestLoadHeaderOnlyReturnsDefaults(t *testing.T) {
	path := writeTmpCSV(t, "question,answer\n")
	items := Load(path)
	if len(items) == 0 {
		t.Fatal("header-only file should return defaults")
	}
}

func TestLoadTrimsWhitespace(t *testing.T) {
	path := writeTmpCSV(t, "  Spaced question?  ,  Spaced answer  \n")
	items := Load(path)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Question != "Spaced question?" {
		t.Errorf("Question not trimmed: %q", items[0].Question)
	}
	if items[0].Answer != "Spaced answer" {
		t.Errorf("Answer not trimmed: %q", items[0].Answer)
	}
}

func TestFetchFromAPITrimsWhitespace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"response_code": 0,
			"results": [{
				"question": "  What is 2+2?  ",
				"correct_answer": "  4  ",
				"incorrect_answers": ["  3  ", "  5  "]
			}]
		}`)
	}))
	defer srv.Close()

	// Temporarily override the API URL by fetching via the test server.
	// FetchFromAPI uses a hardcoded URL, so we call the server directly
	// and parse the same way the production code does.
	items, err := fetchFromURL(nil, srv.URL, 1)
	if err != nil {
		t.Fatalf("fetchFromURL error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Question != "What is 2+2?" {
		t.Errorf("Question not trimmed: %q", items[0].Question)
	}
	if items[0].Answer != "4" {
		t.Errorf("Answer not trimmed: %q", items[0].Answer)
	}
	for _, c := range items[0].Choices {
		if c != "3" && c != "4" && c != "5" {
			t.Errorf("Choice not trimmed: %q", c)
		}
	}
}

func TestDefaultsArePopulated(t *testing.T) {
	if len(defaults) == 0 {
		t.Fatal("defaults should not be empty")
	}
	for i, item := range defaults {
		if item.Question == "" {
			t.Errorf("defaults[%d].Question is empty", i)
		}
		if item.Answer == "" {
			t.Errorf("defaults[%d].Answer is empty", i)
		}
	}
}
