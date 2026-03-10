package trivia

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/zerohalo/weatherrupert/internal/apiurl"
)

// TriviaItem is a single question/answer pair from trivia.csv.
type TriviaItem struct {
	Question   string
	Answer     string
	Choices    []string  // nil for Q&A-only items; 2–4 options (shuffled) for multiple choice
	CategoryID int       // 0 = unknown; 9–32 = Open Trivia DB category ID
	Difficulty string    // "" = unknown; "easy", "medium", "hard"
	FetchedAt  time.Time // when this item was fetched from the API (zero for non-API items)
}

// categoryNameToID maps Open Trivia DB category names to their numeric IDs.
var categoryNameToID = map[string]int{
	"General Knowledge":                     9,
	"Entertainment: Books":                  10,
	"Entertainment: Film":                   11,
	"Entertainment: Music":                  12,
	"Entertainment: Musicals & Theatres":    13,
	"Entertainment: Television":             14,
	"Entertainment: Video Games":            15,
	"Entertainment: Board Games":            16,
	"Science & Nature":                      17,
	"Science: Computers":                    18,
	"Science: Mathematics":                  19,
	"Mythology":                             20,
	"Sports":                                21,
	"Geography":                             22,
	"History":                               23,
	"Politics":                              24,
	"Art":                                   25,
	"Celebrities":                           26,
	"Animals":                               27,
	"Vehicles":                              28,
	"Entertainment: Comics":                 29,
	"Science: Gadgets":                      30,
	"Entertainment: Japanese Anime & Manga": 31,
	"Entertainment: Cartoon & Animations":   32,
}

// defaults are 61 pre-populated trivia items shown when no trivia.csv is found.
var defaults = []TriviaItem{
	// Geography
	{Question: "What is the capital of France?", Answer: "Paris"},
	{Question: "What is the largest ocean on Earth?", Answer: "The Pacific Ocean"},
	{Question: "What is the longest river in the world?", Answer: "The Nile River"},
	{Question: "What is the largest country in the world by area?", Answer: "Russia"},
	{Question: "What is the smallest country in the world?", Answer: "Vatican City"},
	{Question: "What is the capital city of Australia?", Answer: "Canberra"},
	{Question: "What is the largest hot desert in the world?", Answer: "The Sahara Desert"},
	{Question: "What is the tallest mountain in the world?", Answer: "Mount Everest"},
	{Question: "What country has the most natural lakes?", Answer: "Canada"},
	{Question: "What continent is the Amazon River located on?", Answer: "South America"},

	// Science
	{Question: "What planet is closest to the Sun?", Answer: "Mercury"},
	{Question: "What is the chemical symbol for gold?", Answer: "Au"},
	{Question: "How many bones are in the adult human body?", Answer: "206"},
	{Question: "What gas do plants absorb from the atmosphere to make food?", Answer: "Carbon dioxide"},
	{Question: "What element has the atomic number 1?", Answer: "Hydrogen"},
	{Question: "What is the largest organ in the human body?", Answer: "The skin"},
	{Question: "How many moons does Mars have?", Answer: "Two"},
	{Question: "At what temperature does water boil in Celsius?", Answer: "100 degrees Celsius"},
	{Question: "What force keeps planets in orbit around the Sun?", Answer: "Gravity"},
	{Question: "What is the hardest natural substance on Earth?", Answer: "Diamond"},

	// History
	{Question: "In what year did World War II end?", Answer: "1945"},
	{Question: "Who was the first President of the United States?", Answer: "George Washington"},
	{Question: "In what year did humans first land on the Moon?", Answer: "1969"},
	{Question: "Who painted the Mona Lisa?", Answer: "Leonardo da Vinci"},
	{Question: "In what year did the Berlin Wall fall?", Answer: "1989"},
	{Question: "What ancient civilization built the Great Pyramids at Giza?", Answer: "The ancient Egyptians"},
	{Question: "In what country did the Industrial Revolution begin?", Answer: "Great Britain"},
	{Question: "In what year did the Titanic sink?", Answer: "1912"},
	{Question: "Who wrote the Declaration of Independence?", Answer: "Thomas Jefferson"},
	{Question: "What war was fought between the North and South in the United States?", Answer: "The American Civil War"},

	// Nature & Animals
	{Question: "What is the largest land animal on Earth?", Answer: "The African elephant"},
	{Question: "What is the fastest land animal?", Answer: "The cheetah"},
	{Question: "How many legs does a spider have?", Answer: "Eight"},
	{Question: "What is the largest mammal in the world?", Answer: "The blue whale"},
	{Question: "What do you call a group of lions?", Answer: "A pride"},
	{Question: "What is the only mammal capable of true flight?", Answer: "The bat"},
	{Question: "How long is an elephant's pregnancy?", Answer: "About 22 months"},
	{Question: "What is the largest bird in the world?", Answer: "The ostrich"},
	{Question: "What do you call a baby kangaroo?", Answer: "A joey"},
	{Question: "How many hearts does an octopus have?", Answer: "Three"},

	// Literature & Arts
	{Question: "Who wrote Romeo and Juliet?", Answer: "William Shakespeare"},
	{Question: "What is the name of the wizard school in Harry Potter?", Answer: "Hogwarts"},
	{Question: "Who wrote the novel 1984?", Answer: "George Orwell"},
	{Question: "Who wrote the novel Moby-Dick?", Answer: "Herman Melville"},
	{Question: "What artist is famous for cutting off part of his own ear?", Answer: "Vincent van Gogh"},
	{Question: "Who wrote the Adventures of Huckleberry Finn?", Answer: "Mark Twain"},

	// Food & Drink
	{Question: "What country did pizza originate in?", Answer: "Italy"},
	{Question: "What is the main ingredient in guacamole?", Answer: "Avocado"},
	{Question: "What is the most consumed beverage in the world after water?", Answer: "Tea"},
	{Question: "What nut is used to make marzipan?", Answer: "Almonds"},
	{Question: "How many teaspoons are in a tablespoon?", Answer: "Three"},

	// Math & Numbers
	{Question: "What is the value of Pi rounded to two decimal places?", Answer: "3.14"},
	{Question: "How many sides does a hexagon have?", Answer: "Six"},
	{Question: "What is the square root of 144?", Answer: "12"},
	{Question: "How many minutes are in a day?", Answer: "1,440"},
	{Question: "How many degrees are in a right angle?", Answer: "90"},

	// Technology & Culture
	{Question: "What does WWW stand for in a web address?", Answer: "World Wide Web"},
	{Question: "What company created the iPhone?", Answer: "Apple"},
	{Question: "How many strings does a standard guitar have?", Answer: "Six"},
	{Question: "How many rings are on the Olympic flag?", Answer: "Five"},
	{Question: "What sport is played at Wimbledon?", Answer: "Tennis"},
	{Question: "What country has won the most FIFA World Cup titles?", Answer: "Brazil"},
	{Question: "What language has the most native speakers in the world?", Answer: "Mandarin Chinese"},
}

// Load reads trivia items from a CSV file at path.
// Each row must have at least two columns: question and answer. A header row
// whose first field is "question" (case-insensitive) is silently skipped.
// If the file does not exist or cannot be parsed, the built-in defaults are returned.
func Load(path string) []TriviaItem {
	items, err := load(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("trivia: %v (using defaults)", err)
		}
		return defaults
	}
	if len(items) == 0 {
		log.Printf("trivia: %s is empty (using defaults)", path)
		return defaults
	}
	log.Printf("trivia: loaded %d item(s) from %s", len(items), path)
	return items
}

// APIOptions configures the Open Trivia Database API request.
type APIOptions struct {
	Count      int    // desired number of questions (default 50, max 50)
	Category   int    // 0 = any category; 9–32 = specific category ID
	Difficulty string // "" = any; "easy", "medium", "hard"
}

// FetchFromAPI fetches trivia questions from the Open Trivia Database.
// Questions are capped at 100 and fetched in batches of 50.
// Best-effort: logs warnings and returns partial results on error.
func FetchFromAPI(opts APIOptions, httpClient *http.Client) ([]TriviaItem, error) {
	count := opts.Count
	if count <= 0 {
		return nil, nil
	}
	if count > 50 {
		count = 50
	}

	var items []TriviaItem
	remaining := count

	for remaining > 0 {
		batch := remaining
		if batch > 50 {
			batch = 50
		}
		url := fmt.Sprintf("%s?amount=%d", apiurl.OpenTriviaDB, batch)
		if opts.Category > 0 {
			url += fmt.Sprintf("&category=%d", opts.Category)
		}
		if opts.Difficulty != "" {
			url += "&difficulty=" + opts.Difficulty
		}

		fetched, err := fetchFromURL(httpClient, url, batch)
		if err != nil {
			log.Printf("trivia: %v", err)
			break
		}
		items = append(items, fetched...)

		remaining -= batch
		// Brief pause between requests to be polite to the API.
		if remaining > 0 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no questions fetched from Open Trivia Database")
	}
	return items, nil
}

// fetchFromURL fetches up to batch trivia questions from the given URL.
func fetchFromURL(httpClient *http.Client, url string, batch int) ([]TriviaItem, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %v", err)
	}

	var result struct {
		ResponseCode int `json:"response_code"`
		Results      []struct {
			Category         string   `json:"category"`
			Difficulty       string   `json:"difficulty"`
			Question         string   `json:"question"`
			CorrectAnswer    string   `json:"correct_answer"`
			IncorrectAnswers []string `json:"incorrect_answers"`
		} `json:"results"`
	}
	err = json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("API decode failed: %v", err)
	}
	if result.ResponseCode != 0 {
		return nil, fmt.Errorf("API returned code %d", result.ResponseCode)
	}

	now := time.Now()
	var items []TriviaItem
	for _, q := range result.Results {
		choices := make([]string, 0, 4)
		for _, inc := range q.IncorrectAnswers {
			choices = append(choices, strings.TrimSpace(html.UnescapeString(inc)))
		}
		correct := strings.TrimSpace(html.UnescapeString(q.CorrectAnswer))
		choices = append(choices, correct)
		rand.Shuffle(len(choices), func(i, j int) { choices[i], choices[j] = choices[j], choices[i] })

		items = append(items, TriviaItem{
			Question:   strings.TrimSpace(html.UnescapeString(q.Question)),
			Answer:     correct,
			Choices:    choices,
			CategoryID: categoryNameToID[html.UnescapeString(q.Category)],
			Difficulty: q.Difficulty,
			FetchedAt:  now,
		})
	}
	return items, nil
}

func load(path string) ([]TriviaItem, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	r.Comment = '#'

	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}

	var items []TriviaItem
	for i, row := range rows {
		if len(row) < 2 {
			continue
		}
		question := strings.TrimSpace(row[0])
		answer := strings.TrimSpace(row[1])
		if question == "" || answer == "" {
			continue
		}
		// Skip an optional header row.
		if i == 0 && strings.EqualFold(question, "question") {
			continue
		}
		items = append(items, TriviaItem{Question: question, Answer: answer})
	}
	return items, nil
}
