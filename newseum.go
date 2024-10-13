package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"regexp"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mmcdole/gofeed"
	"github.com/rivo/tview"
)

type FeedSource struct {
	Name string
	URL  string
}

type FeedItem struct {
	Title     string
	Date      time.Time
	FeedTitle string
	Link      string
	AudioURL  string
}

func main() {
    fmt.Print("\033[H\033[2J")

	feedSources, err := getFeedSources()
	if err != nil {
		fmt.Println(err)
		return
	}

	items, err := fetchFeeds(feedSources)
	if err != nil {
		fmt.Println("Error fetching feeds:", err)
		return
	}

	app := tview.NewApplication()
	table := tview.NewTable().SetSelectable(true, false)
	table.SetBackgroundColor(tcell.ColorDefault)

	now := time.Now().UTC() // Use UTC for consistency
	for i, item := range items {
		dateStr := " " + formatDate(item.Date, now)
		titleStr := FormatString(" " + CleanString(item.Title), 75)
		feedStr := FormatString(" " + CleanString(item.FeedTitle), 25)

		title := tview.NewTableCell(titleStr).SetTextColor(tcell.GetColor("red"))
		feed := tview.NewTableCell(feedStr).SetTextColor(tcell.GetColor("green"))

		table.SetCell(i, 0, feed)
		table.SetCell(i, 1, title)
		table.SetCellSimple(i, 2, dateStr)
	}

	table.Select(0, 0).SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			app.Stop()
		}
	}).SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case 'q':
			app.Stop()
			return nil
		case 'g':
			table.Select(0, 0)
			table.ScrollToBeginning()
		case 'G':
			table.Select(len(items)-1, 0)
			table.ScrollToEnd()
		}
		return event
	})

	app.SetMouseCapture(func(event *tcell.EventMouse, action tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
		if action == tview.MouseScrollDown {
			app.QueueEvent(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))
			return nil, 0 // Consume the event
		} else if action == tview.MouseScrollUp {
			app.QueueEvent(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone))
			return nil, 0 // Consume the event
		}
		return event, action
	})

    table.SetSelectedFunc(func(row, column int) {
        if row >= 0 && row < len(items) {
            var url string
            if items[row].AudioURL != "" {
                url = items[row].AudioURL
            } else {
                url = items[row].Link
            }
            err := openBrowser(url)
            if err != nil {
                fmt.Println("Error opening browser:", err)
            }
        }
    })

	if err := app.SetRoot(table, true).EnableMouse(true).Run(); err != nil {
		panic(err)
	}
}

func formatDate(date time.Time, now time.Time) string {
    if date.IsZero() {
        return "Unknown date"
    }

    // Convert UTC time to local time
    localDate := date.Local()
    localNow := now.Local()

    duration := localNow.Sub(localDate)
    if duration < 24*time.Hour && localDate.Day() == localNow.Day() {
        return "Today at " + localDate.Format("3:04 PM")
    } else if duration < 48*time.Hour && localDate.Day() == localNow.AddDate(0, 0, -1).Day() {
        return "Yesterday at " + localDate.Format("3:04 PM")
    } else if duration < 7*24*time.Hour {
        return localDate.Format("Monday at 3:04 PM")
    } else {
        return localDate.Format("January 2, 2006")
    }
}

func CleanString(input string) string {
	whitespaceRegex := regexp.MustCompile(`\s+`)
	trimmed := whitespaceRegex.ReplaceAllString(input, " ")
    trimmed = strings.ReplaceAll(trimmed, "[", "(")
    trimmed = strings.ReplaceAll(trimmed, "]", ")")

	return strings.TrimSpace(trimmed)
}

func FormatString(s string, length int) string {
	if len(s) > length {
		return s[:length] // Truncate to specified length
	} else if len(s) < length {
		return s + strings.Repeat(" ", length-len(s)) // Add spaces to make it specified length
	}
	return s
}

func getFeedSources() ([]FeedSource, error) {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("unable to determine home directory: %v", err)
		}
		configDir = filepath.Join(homeDir, ".config")
	}

	filePath := filepath.Join(configDir, "newseum", "feeds.csv")
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("error opening file %s: %v\nPlease create the file and fill it with a CSV list of feed names and URLs", filePath, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = 2 // Expect 2 fields per record: name and URL

	var feedSources []FeedSource
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error reading CSV: %v", err)
		}
		feedSources = append(feedSources, FeedSource{
			Name: strings.TrimSpace(record[0]),
			URL:  strings.TrimSpace(record[1]),
		})
	}

	return feedSources, nil
}

func fetchFeeds(feedSources []FeedSource) ([]FeedItem, error) {
    var items []FeedItem
    fp := gofeed.NewParser()

    totalFeeds := len(feedSources)
    for i, source := range feedSources {
        fmt.Printf("\rFetching %d/%d feeds...", i+1, totalFeeds)

        feed, err := fp.ParseURL(source.URL)
        if err != nil {
            fmt.Printf("\nError parsing feed %s: %v\n", source.URL, err)
            continue
        }

        feedTitle := source.Name
        if feedTitle == "" {
            feedTitle = feed.Title
        }

        for _, item := range feed.Items {
            pubDate := time.Now().UTC() // Default to current UTC time
            if item.PublishedParsed != nil {
                pubDate = item.PublishedParsed.UTC() // Ensure the time is in UTC
            }

            audioURL := ""
            for _, enclosure := range item.Enclosures {
                if strings.HasPrefix(enclosure.Type, "audio/") {
                    audioURL = enclosure.URL
                    break
                }
            }

            items = append(items, FeedItem{
                Title:     item.Title,
                Date:      pubDate,
                FeedTitle: feedTitle,
                Link:      item.Link,
                AudioURL:  audioURL,
            })
        }
    }

    fmt.Println("\rFinished fetching all feeds.           ")

    sort.Slice(items, func(i, j int) bool {
        return items[i].Date.After(items[j].Date)
    })

    return items, nil
}

func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	case "darwin":
		cmd = "open"
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}
