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
	Title       string
	Date        time.Time
	FeedTitle   string
	Link        string
	AudioURL    string
	Description string
	SearchText  string
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

	// Preview pane
	preview := tview.NewTextView().
		SetDynamicColors(true).
		SetWordWrap(true).
		SetScrollable(true)
	preview.SetBackgroundColor(tcell.ColorDefault)
	preview.SetBorder(true).SetTitle(" Preview ")

	// Search state
	var searchQuery string
	filteredItems := items

	// Function to update table with current items
	updateTable := func(itemsToShow []FeedItem) {
		table.Clear()
		now := time.Now().UTC()
		for i, item := range itemsToShow {
			dateStr := " " + formatDate(item.Date, now)
			titleStr := FormatString(" "+CleanString(item.Title), 75)
			feedStr := FormatString(" "+CleanString(item.FeedTitle), 25)

			title := tview.NewTableCell(titleStr).SetTextColor(tcell.GetColor("red"))
			feed := tview.NewTableCell(feedStr).SetTextColor(tcell.GetColor("green"))

			table.SetCell(i, 0, feed)
			table.SetCell(i, 1, title)
			table.SetCellSimple(i, 2, dateStr)
		}
		if len(itemsToShow) > 0 {
			table.Select(0, 0)
		}
	}

	// Function to update preview
	updatePreview := func() {
		row, _ := table.GetSelection()
		if row >= 0 && row < len(filteredItems) {
			item := filteredItems[row]
			previewText := fmt.Sprintf("[yellow]%s[-]\n\n[green]%s[-]\n%s\n\n%s",
				item.Title,
				item.FeedTitle,
				formatDate(item.Date, time.Now().UTC()),
				CleanString(item.Description))
			preview.SetText(previewText)
			preview.ScrollToBeginning()
		}
	}

	updateTable(filteredItems)
	updatePreview()

	// Search input field
	searchInput := tview.NewInputField().SetLabel("")
	searchInput.SetBackgroundColor(tcell.Color16)
    searchInput.SetFieldStyle(tcell.StyleDefault.Background(tcell.Color16).Foreground(tcell.ColorWhite))
	searchInput.SetLabelStyle(tcell.StyleDefault.Background(tcell.Color16).Foreground(tcell.ColorWhite))

	searchInput.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			searchQuery = ""
			searchInput.SetText("")
			searchInput.SetLabel("")
			filteredItems = items
			updateTable(filteredItems)
			updatePreview()
			table.Select(0, 0)
			table.ScrollToBeginning()
			app.SetFocus(table)
		} else if key == tcell.KeyEnter {
			app.SetFocus(table)
		}
	})

searchInput.SetChangedFunc(func(text string) {
		searchQuery = strings.ToLower(text)
		if searchQuery == "" {
			filteredItems = items
		} else {
			// This is much faster as it searches pre-computed, lower-cased text
			newFilteredItems := make([]FeedItem, 0, len(filteredItems)) // Pre-allocate for efficiency
			for _, item := range items {
				if strings.Contains(item.SearchText, searchQuery) {
					newFilteredItems = append(newFilteredItems, item)
				}
			}
			filteredItems = newFilteredItems
		}
		updateTable(filteredItems)
		updatePreview()
	})

	table.SetSelectionChangedFunc(func(row, column int) {
		updatePreview()
	})

	table.SetDoneFunc(func(key tcell.Key) {
		if key == 'q' {
			app.Stop()
		}
	}).SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		row, _ := table.GetSelection()
		
		// Block upward movement at the top
		if row == 0 && (event.Key() == tcell.KeyUp || event.Rune() == 'k') {
			return nil
		}
		
		// Block downward movement at the bottom
		if row == len(filteredItems)-1 && (event.Key() == tcell.KeyDown || event.Rune() == 'j') {
			return nil
		}

		if event.Key() == tcell.KeyEscape {
			searchQuery = ""
			searchInput.SetText("")
			searchInput.SetLabel("")
			filteredItems = items
			updateTable(filteredItems)
			updatePreview()
			table.Select(0, 0)
			table.ScrollToBeginning()
		}

		switch event.Rune() {
		case 'q':
			app.Stop()
			return nil
		case 'g':
			if len(filteredItems) > 0 {
				table.Select(0, 0)
				table.ScrollToBeginning()
			}
			return nil
		case 'G':
			if len(filteredItems) > 0 {
				table.Select(len(filteredItems)-1, 0)
				table.ScrollToEnd()
			}
			return nil
		case '/':
			searchInput.SetLabel("/")
			searchInput.SetText("")
			searchQuery = ""
			app.SetFocus(searchInput)
			return nil
		}
		return event
	})

	var lastScrollTime time.Time
	scrollThrottle := 10 * time.Millisecond

	app.SetMouseCapture(func(event *tcell.EventMouse, action tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
		// Throttle scroll events to prevent overwhelming the event loop
		if action == tview.MouseScrollDown || action == tview.MouseScrollUp {
			now := time.Now()
			if now.Sub(lastScrollTime) < scrollThrottle {
				return nil, 0 // Ignore this scroll event
			}
			lastScrollTime = now
		}
		
		row, _ := table.GetSelection()
		
		if action == tview.MouseScrollDown {
			if row < len(filteredItems)-1 {
				table.Select(row+1, 0)
			}
			return nil, 0
		} else if action == tview.MouseScrollUp {
			if row > 0 {
				table.Select(row-1, 0)
			}
			return nil, 0
		}
		return event, action
	})

    table.SetSelectedFunc(func(row, column int) {
        if row >= 0 && row < len(filteredItems) {
            var url string
            if filteredItems[row].AudioURL != "" {
                url = filteredItems[row].AudioURL
            } else {
                url = filteredItems[row].Link
            }
            err := openURL(url)
            if err != nil {
                fmt.Println("Error opening browser:", err)
            }
        }
    })

	// Layout: table and preview side by side
	contentFlex := tview.NewFlex().
		AddItem(table, 0, 2, true).
		AddItem(preview, 0, 1, false)

	// Main layout: content on top, search on bottom
	mainFlex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(contentFlex, 0, 1, true).
		AddItem(searchInput, 1, 0, false)

	if err := app.SetRoot(mainFlex, true).EnableMouse(true).Run(); err != nil {
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
	runes := []rune(s)
	runeCount := len(runes)
	
	if runeCount > length {
		return string(runes[:length]) // Truncate to specified length
	} else if runeCount < length {
		return s + strings.Repeat(" ", length-runeCount) // Add spaces
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

    jobs := make(chan FeedSource, len(feedSources))
    results := make(chan error, len(feedSources))
    
    workers := 5
    
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

                    description := ""
                    if item.Description != "" {
                        description = item.Description
                    } else if item.Content != "" {
                        description = item.Content
                    }

					searchable := strings.Builder{}
					searchable.WriteString(strings.ToLower(item.Title))
					searchable.WriteString(" ")
					searchable.WriteString(strings.ToLower(feedTitle))
					searchable.WriteString(" ")
					searchable.WriteString(strings.ToLower(description))

                    feedItems = append(feedItems, FeedItem{
                        Title:       item.Title,
                        Date:        pubDate,
                        FeedTitle:   feedTitle,
                        Link:        fixNitterLink(source.URL, item.Link),
                        AudioURL:    audioURL,
                        Description: description,
						SearchText:  searchable.String(),
                    })
                }

                mutex.Lock()
                items = append(items, feedItems...)
                mutex.Unlock()
                
                results <- nil
            }
        }()
    }

    // Send all jobs
    for _, source := range feedSources {
        jobs <- source
    }
    close(jobs)

    // Start goroutine to close results channel when workers are done
    go func() {
        wg.Wait()
        close(results)
    }()

    // Collect results and display progress
    totalFeeds := len(feedSources)
    progress := 0
    for err := range results {
        progress++
        if err != nil {
            fmt.Printf("\n%v", err)
        }
        fmt.Printf("\rFetching %d/%d feeds...", progress, totalFeeds)
    }
    
    fmt.Println("\rFinished fetching all feeds.           ")

    // Sort items by date
    sort.Slice(items, func(i, j int) bool {
        return items[i].Date.After(items[j].Date)
    })

    return items, nil
}

func fixNitterLink(feedURL, itemLink string) string {
    // If feed is from a nitter instance, convert item links to x.com
    if strings.Contains(feedURL, "nitter.") {
        // Extract the path from any domain (nitter or not)
        re := regexp.MustCompile(`https?://[^/]+/(.*)`)
        if matches := re.FindStringSubmatch(itemLink); len(matches) > 1 {
            // Remove hash fragment if present
            path := strings.Split(matches[1], "#")[0]
            return "https://x.com/" + path
        }
    }
    return itemLink
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
