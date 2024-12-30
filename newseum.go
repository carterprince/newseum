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
	"sync"

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
    table.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorWhite).Foreground(tcell.ColorBlack))

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
            err := openURL(url)
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
    var mutex sync.Mutex
    fp := gofeed.NewParser()

    // Create channels for work distribution and results
    jobs := make(chan FeedSource)
    results := make(chan error)
    
    // Number of concurrent workers (can be adjusted)
    workers := 5
    
    // Start worker pool
    var wg sync.WaitGroup
    for w := 0; w < workers; w++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for source := range jobs {
                feed, err := fp.ParseURL(source.URL)
                if err != nil {
                    results <- fmt.Errorf("error parsing feed %s: %v", source.URL, err)
                    continue
                }

                feedTitle := source.Name
                if feedTitle == "" {
                    feedTitle = feed.Title
                }

                var feedItems []FeedItem
                for _, item := range feed.Items {
                    pubDate := time.Now().UTC()
                    if item.PublishedParsed != nil {
                        pubDate = item.PublishedParsed.UTC()
                    }

                    audioURL := ""
                    for _, enclosure := range item.Enclosures {
                        if strings.HasPrefix(enclosure.Type, "audio/") {
                            audioURL = enclosure.URL
                            break
                        }
                    }

                    feedItems = append(feedItems, FeedItem{
                        Title:     item.Title,
                        Date:      pubDate,
                        FeedTitle: feedTitle,
                        Link:      item.Link,
                        AudioURL:  audioURL,
                    })
                }

                mutex.Lock()
                items = append(items, feedItems...)
                mutex.Unlock()
                
                results <- nil
            }
        }()
    }

    // Create progress counter
    progress := 0
    totalFeeds := len(feedSources)
    
    // Start a goroutine to distribute work
    go func() {
        for _, source := range feedSources {
            jobs <- source
        }
        close(jobs)
    }()

    // Start a goroutine to collect results and update progress
    go func() {
        for range feedSources {
            err := <-results
            progress++
            if err != nil {
                fmt.Printf("\n%v", err)
            }
            fmt.Printf("\rFetching %d/%d feeds...", progress, totalFeeds)
        }
        wg.Wait()
        close(results)
    }()

    // Wait for all workers to complete
    wg.Wait()
    fmt.Println("\rFinished fetching all feeds.           ")

    // Sort items by date
    sort.Slice(items, func(i, j int) bool {
        return items[i].Date.After(items[j].Date)
    })

    return items, nil
}

func openURL(url string) error {
    lowerURL := strings.ToLower(url)
    
    // Check for media URLs
    isAudio := regexp.MustCompile(`\.(mp3|wav)(?:\?.*)?$`).MatchString(lowerURL)
    isYoutube := strings.Contains(lowerURL, "youtube.com") || strings.Contains(lowerURL, "youtu.be")
    
    if (isAudio || isYoutube) && runtime.GOOS == "linux" {
        var mimeType string
        if isYoutube {
            mimeType = "video/mp4" // More appropriate for YouTube content
        } else if strings.Contains(lowerURL, ".mp3") {
            mimeType = "audio/mpeg"
        } else {
            mimeType = "audio/wav"
        }

        // Get default application for media type
        cmd := exec.Command("xdg-mime", "query", "default", mimeType)
        output, err := cmd.Output()
        if err != nil {
            return fmt.Errorf("error querying default media application: %v", err)
        }
        
        desktopFile := strings.TrimSpace(string(output))
        if desktopFile == "" {
            return fmt.Errorf("no default application found for %s", mimeType)
        }

        // Launch the media file with the default application
        return exec.Command("gtk-launch", desktopFile, url).Start()
    }

    // For non-media files or non-Linux systems, use the original browser opening logic
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
