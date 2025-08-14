package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ViewMode represents the current view mode
type ViewMode int

const (
	ViewBrowser ViewMode = iota
	ViewPreview
	ViewHelp
	ViewUpload
)

// LocalItem represents a local file or directory
type LocalItem struct {
	Name  string
	IsDir bool
	Size  int64
}

// Model represents the application state
type Model struct {
	s3Client        *S3Client
	bucket          string
	currentPath     string
	objects         []S3Object
	cursor          int
	viewMode        ViewMode
	previewContent  string
	previewFileName string
	previewLines    []string
	previewScroll   int
	previewWidth    int
	localItems      []LocalItem
	localPath       string
	err             error
	statusMessage   string
	loading         bool
	width           int
	height          int
}

// Messages for async operations
type objectsLoadedMsg struct {
	objects []S3Object
	err     error
}

type previewLoadedMsg struct {
	content string
	file    string
	err     error
}

type fileDownloadedMsg struct {
	filename string
	err      error
}

type fileUploadedMsg struct {
	filename string
	err      error
}

type fileDeletedMsg struct {
	filename string
	err      error
}

type statusMsg struct {
	message string
	isError bool
}

type localFilesLoadedMsg struct {
	items []LocalItem
	path  string
	err   error
}

type errorMsg struct {
	err error
}

// Styles - Minimalistic theme
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#ffffff")).
			Background(lipgloss.Color("#333333")).
			Padding(0, 1)

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#ffffff")).
			Padding(0, 1)

	directoryStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#0066cc")).
			Bold(true)

	fileStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#bbbbbb"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#cc0000")).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#006600")).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666"))

	previewStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#999999")).
			Padding(1, 2)

	browserStyle = lipgloss.NewStyle().
		// Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#999999")).
		Padding(1, 2).
		Align(lipgloss.Center)

	centerStyle = lipgloss.NewStyle().
			Align(lipgloss.Center)

	verticalCenterStyle = lipgloss.NewStyle().
				AlignVertical(lipgloss.Center)
)

// NewModel creates a new TUI model
func NewModel(s3Client *S3Client, bucket string) Model {
	return Model{
		s3Client:    s3Client,
		bucket:      bucket,
		currentPath: "",
		objects:     []S3Object{},
		cursor:      0,
		viewMode:    ViewBrowser,
		loading:     true,
	}
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return m.loadObjects()
}

// Update handles messages and updates the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch m.viewMode {
		case ViewBrowser:
			return m.updateBrowser(msg)
		case ViewPreview:
			return m.updatePreview(msg)
		case ViewHelp:
			return m.updateHelp(msg)
		case ViewUpload:
			return m.updateUpload(msg)
		}

	case objectsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.objects = msg.objects
			m.cursor = 0
			m.err = nil
		}
		return m, nil

	case previewLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.previewContent = msg.content
			m.previewFileName = msg.file
			m.previewLines = strings.Split(msg.content, "\n")
			m.previewScroll = 0
			m.previewWidth = m.calculatePreviewWidth()
			m.viewMode = ViewPreview
			m.err = nil
		}
		return m, nil

	case fileDownloadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.statusMessage = ""
		} else {
			m.err = nil
			m.statusMessage = fmt.Sprintf("✓ Downloaded '%s' successfully", msg.filename)
		}
		return m, nil

	case fileUploadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.statusMessage = ""
		} else {
			m.err = nil
			m.statusMessage = fmt.Sprintf("✓ Uploaded '%s' successfully", msg.filename)
			// Refresh the directory to show the new file
			return m, m.loadObjects()
		}
		return m, nil

	case localFilesLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.localItems = msg.items
			m.localPath = msg.path
			m.cursor = 0
			m.viewMode = ViewUpload
			m.err = nil
		}
		return m, nil

	case fileDeletedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.statusMessage = ""
		} else {
			m.err = nil
			m.statusMessage = fmt.Sprintf("✓ Deleted '%s' successfully", msg.filename)
			// Refresh the directory to remove the deleted file
			return m, m.loadObjects()
		}
		return m, nil

	case statusMsg:
		if msg.isError {
			m.err = fmt.Errorf(msg.message)
			m.statusMessage = ""
		} else {
			m.err = nil
			m.statusMessage = msg.message
		}
		return m, nil

	case errorMsg:
		m.err = msg.err
		m.loading = false
		return m, nil
	}

	return m, nil
}

// updateBrowser handles browser view updates
func (m Model) updateBrowser(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "j":
		if m.cursor < len(m.objects)-1 {
			m.cursor++
		}

	case "enter", "l", "o":
		if len(m.objects) > 0 {
			selected := m.objects[m.cursor]
			if selected.IsDir {
				// Navigate into directory
				m.currentPath = selected.Key
				m.loading = true
				return m, m.loadObjects()
			} else {
				// Preview file
				return m, m.previewFileContent(selected.Key)
			}
		}

	case "backspace", "h":
		// Go back to parent directory
		if m.currentPath != "" {
			parts := strings.Split(m.currentPath, "/")
			if len(parts) > 1 {
				m.currentPath = strings.Join(parts[:len(parts)-1], "/")
			} else {
				m.currentPath = ""
			}
			m.loading = true
			return m, m.loadObjects()
		}

	case "r":
		// Refresh current directory
		m.loading = true
		return m, m.loadObjects()

	case "d":
		// Download selected file
		if len(m.objects) > 0 {
			selected := m.objects[m.cursor]
			if !selected.IsDir {
				return m, m.downloadFile(selected.Key)
			}
		}

	case "u":
		// Upload file from current directory
		return m, m.uploadFilePrompt()

	case "x":
		// Delete selected file
		if len(m.objects) > 0 {
			selected := m.objects[m.cursor]
			if !selected.IsDir {
				return m, m.deleteFile(selected.Key)
			}
		}

	case "?":
		m.viewMode = ViewHelp
	}

	return m, nil
}

// updatePreview handles preview view updates
func (m Model) updatePreview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "backspace", "h", "left":
		m.viewMode = ViewBrowser
		m.previewContent = ""
		m.previewFileName = ""
		m.previewLines = nil
		m.previewScroll = 0
		m.previewWidth = 0
	case "up", "k":
		if m.previewScroll > 0 {
			m.previewScroll--
		}
	case "down", "j":
		maxScroll := len(m.previewLines) - (m.height - 8) // Account for title, borders, help
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.previewScroll < maxScroll {
			m.previewScroll++
		}
	case "pgup", "u":
		m.previewScroll -= 10
		if m.previewScroll < 0 {
			m.previewScroll = 0
		}
	case "pgdown", "d":
		maxScroll := len(m.previewLines) - (m.height - 8)
		if maxScroll < 0 {
			maxScroll = 0
		}
		m.previewScroll += 10
		if m.previewScroll > maxScroll {
			m.previewScroll = maxScroll
		}
	case "home", "g":
		m.previewScroll = 0
	case "end", "G":
		maxScroll := len(m.previewLines) - (m.height - 8)
		if maxScroll < 0 {
			maxScroll = 0
		}
		m.previewScroll = maxScroll
	}
	return m, nil
}

// updateHelp handles help view updates
func (m Model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "?":
		m.viewMode = ViewBrowser
	}
	return m, nil
}

// updateUpload handles upload view updates
func (m Model) updateUpload(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		m.viewMode = ViewBrowser
		return m, nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.localItems)-1 {
			m.cursor++
		}
	case "enter", "l", "o":
		if len(m.localItems) > 0 {
			selected := m.localItems[m.cursor]
			if selected.IsDir {
				// Navigate into directory
				var newPath string
				if selected.Name == ".." {
					// Go to parent directory
					newPath = filepath.Dir(m.localPath)
					if newPath == "." && m.localPath == "." {
						// Go to parent of current working directory
						newPath = ".."
					} else if newPath == "" || newPath == "/" {
						newPath = "."
					}
				} else {
					newPath = filepath.Join(m.localPath, selected.Name)
				}
				return m, m.loadLocalFiles(newPath)
			} else {
				// Upload file
				fullPath := filepath.Join(m.localPath, selected.Name)
				m.viewMode = ViewBrowser
				return m, m.uploadFile(fullPath)
			}
		}
	case "backspace", "h":
		// Go back to parent directory
		parentPath := filepath.Dir(m.localPath)
		if parentPath == "." && m.localPath == "." {
			// Go to parent of current working directory
			parentPath = ".."
		} else if parentPath == "" || parentPath == "/" {
			parentPath = "."
		}
		return m, m.loadLocalFiles(parentPath)
	}
	return m, nil
}

// View renders the current view
func (m Model) View() string {
	switch m.viewMode {
	case ViewBrowser:
		return m.viewBrowser()
	case ViewPreview:
		return m.viewPreview()
	case ViewHelp:
		return m.viewHelp()
	case ViewUpload:
		return m.viewUpload()
	}
	return ""
}

// viewBrowser renders the file browser view
func (m Model) viewBrowser() string {
	var s strings.Builder

	// Title
	title := fmt.Sprintf("Bucket: %s", m.bucket)
	if m.currentPath != "" {
		title += fmt.Sprintf(" | Path: /%s", m.currentPath)
	}
	s.WriteString(titleStyle.Render(title))
	s.WriteString("\n\n")

	// Status and error display
	if m.err != nil {
		s.WriteString(errorStyle.Render(fmt.Sprintf("Error: %s", m.err.Error())))
		s.WriteString("\n\n")
	} else if m.statusMessage != "" {
		s.WriteString(successStyle.Render(m.statusMessage))
		s.WriteString("\n\n")
	}

	// Loading indicator
	if m.loading {
		s.WriteString("Loading...\n")
	} else {
		// File list
		if len(m.objects) == 0 {
			s.WriteString("No objects found in this location.\n")
		} else {
			for i, obj := range m.objects {
				cursor := " "
				if i == m.cursor {
					cursor = ">"
				}

				var line string
				if obj.IsDir {
					name := filepath.Base(obj.Key) + "/"
					line = fmt.Sprintf("%s %s", cursor, directoryStyle.Render(name))
				} else {
					name := filepath.Base(obj.Key)
					size := formatSize(obj.Size)
					line = fmt.Sprintf("%s %s (%s) %s", cursor, fileStyle.Render(name), size, obj.LastModified)
				}

				if i == m.cursor {
					line = selectedStyle.Render(line)
				}

				s.WriteString(line)
				s.WriteString("\n")
			}
		}
	}

	// Help text
	s.WriteString("\n")
	s.WriteString(helpStyle.Render("↑/k: up • ↓/j: down • ←/h: back • →/l/o/enter: select • d: download • u: upload • x: delete • r: refresh • ?: help • q: quit"))

	// Wrap content in border and center it
	content := s.String()
	bordered := browserStyle.Render(content)

	// Center the content on screen
	if m.width > 0 && m.height > 0 {
		centered := centerStyle.Width(m.width).Render(bordered)
		return verticalCenterStyle.Height(m.height).Render(centered)
	}
	return bordered
}

// viewPreview renders the file preview view
func (m Model) viewPreview() string {
	var s strings.Builder

	title := fmt.Sprintf("Preview: %s", m.previewFileName)
	s.WriteString(titleStyle.Render(title))
	s.WriteString("\n\n")

	if m.err != nil {
		s.WriteString(errorStyle.Render(fmt.Sprintf("Error: %s", m.err.Error())))
	} else {
		// Calculate visible lines
		visibleHeight := m.height - 8 // Account for title, borders, help
		if visibleHeight < 1 {
			visibleHeight = 10
		}

		var visibleLines []string
		totalLines := len(m.previewLines)

		if totalLines == 0 {
			visibleLines = []string{"[Empty file]"}
		} else {
			start := m.previewScroll
			end := start + visibleHeight
			if end > totalLines {
				end = totalLines
			}
			if start < totalLines {
				visibleLines = m.previewLines[start:end]
			}
		}

		// Add line numbers and content
		var contentBuilder strings.Builder
		for i, line := range visibleLines {
			lineNum := m.previewScroll + i + 1
			contentBuilder.WriteString(fmt.Sprintf("%4d │ %s\n", lineNum, line))
		}

		// Show scroll indicator if needed
		if totalLines > visibleHeight {
			contentBuilder.WriteString(fmt.Sprintf("\n[Showing lines %d-%d of %d]",
				m.previewScroll+1,
				m.previewScroll+len(visibleLines),
				totalLines))
		}

		// Create a preview style with calculated width
		previewStyleWithWidth := previewStyle.Width(m.previewWidth - 8) // Account for padding and borders
		s.WriteString(previewStyleWithWidth.Render(contentBuilder.String()))
	}

	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("↑/k,↓/j: scroll • u/d: page up/down • g/G: top/bottom • ←/h/esc: back • q: quit"))

	// Center the preview content
	content := s.String()
	if m.width > 0 && m.height > 0 {
		centered := centerStyle.Width(m.width).Render(content)
		return verticalCenterStyle.Height(m.height).Render(centered)
	}
	return content
}

// viewHelp renders the help view
func (m Model) viewHelp() string {
	var s strings.Builder

	s.WriteString(titleStyle.Render("S4 - Help"))
	s.WriteString("\n\n")

	help := `Navigation:
  ↑/k         Move cursor up
  ↓/j         Move cursor down
  ←/h         Go back to parent directory
  →/l/o/enter Enter directory or preview file
  r           Refresh current directory

Actions:
  ?           Show this help
  q/ctrl+c    Quit application

File Operations:
  enter/l/o   Preview text files or enter directories
  d           Download selected file to current directory
  u           Upload file from current directory
  x           Delete selected file from S3

Preview Navigation:
  ↑/k,↓/j     Scroll line by line
  u/d         Page up/down (10 lines)
  g/G         Jump to top/bottom
  ←/h/esc     Return to browser
  
Browser Features:
  - Navigate S3 bucket like a file system
  - Preview text files in-place
  - Download files to local directory
  - Upload files from local directory
  - Shows file sizes and modification dates
  - Distinguishes directories from files

Configuration:
  S4 reads configuration from .s3cfg file in:
  - Current directory
  - Home directory (~/.s3cfg)
  - System directory (/etc/s3cfg)
`

	s.WriteString(help)
	s.WriteString("\n")
	s.WriteString(helpStyle.Render("esc/?: back • q: quit"))

	// Center the help content
	content := s.String()
	if m.width > 0 && m.height > 0 {
		centered := centerStyle.Width(m.width).Render(content)
		return verticalCenterStyle.Height(m.height).Render(centered)
	}
	return content
}

// viewUpload renders the upload file selection view
func (m Model) viewUpload() string {
	var s strings.Builder

	// Show absolute path for better clarity
	displayPath := m.localPath
	if absPath, err := filepath.Abs(m.localPath); err == nil {
		displayPath = absPath
	}
	
	title := fmt.Sprintf("Local: %s", displayPath)
	if m.currentPath != "" {
		title += fmt.Sprintf(" → S3: /%s", m.currentPath)
	} else {
		title += " → S3: /"
	}
	s.WriteString(titleStyle.Render(title))
	s.WriteString("\n\n")

	// Error display
	if m.err != nil {
		s.WriteString(errorStyle.Render(fmt.Sprintf("Error: %s", m.err.Error())))
		s.WriteString("\n\n")
	}

	// File/directory list
	if len(m.localItems) == 0 {
		s.WriteString("No files or directories found.\n")
	} else {
		for i, item := range m.localItems {
			cursor := " "
			if i == m.cursor {
				cursor = ">"
			}

			var line string
			if item.IsDir {
				name := item.Name + "/"
				line = fmt.Sprintf("%s %s", cursor, directoryStyle.Render(name))
			} else {
				size := formatSize(item.Size)
				line = fmt.Sprintf("%s %s (%s)", cursor, fileStyle.Render(item.Name), size)
			}

			if i == m.cursor {
				line = selectedStyle.Render(line)
			}

			s.WriteString(line)
			s.WriteString("\n")
		}
	}

	// Help text
	s.WriteString("\n")
	s.WriteString(helpStyle.Render("↑/k: up • ↓/j: down • ←/h: back • →/l/o/enter: select • esc: cancel • q: quit"))

	// Wrap content in border and center it
	content := s.String()
	bordered := browserStyle.Render(content)

	// Center the upload view on screen
	if m.width > 0 && m.height > 0 {
		centered := centerStyle.Width(m.width).Render(bordered)
		return verticalCenterStyle.Height(m.height).Render(centered)
	}
	return bordered
}

// loadObjects loads objects from S3
func (m Model) loadObjects() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		prefix := m.currentPath
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}

		objects, err := m.s3Client.ListObjects(context.Background(), m.bucket, prefix)
		if err != nil {
			return objectsLoadedMsg{err: err}
		}

		// Sort objects: directories first, then files, both alphabetically
		sort.Slice(objects, func(i, j int) bool {
			if objects[i].IsDir != objects[j].IsDir {
				return objects[i].IsDir
			}
			return objects[i].Key < objects[j].Key
		})

		return objectsLoadedMsg{objects: objects}
	})
}

// previewFileContent loads file content for preview
func (m Model) previewFileContent(key string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		data, err := m.s3Client.GetObject(context.Background(), m.bucket, key)
		if err != nil {
			return previewLoadedMsg{err: err}
		}

		// Check if content is text (simple heuristic)
		if !utf8.Valid(data) {
			return previewLoadedMsg{
				content: "[Binary file - cannot preview]",
				file:    key,
			}
		}

		return previewLoadedMsg{
			content: string(data),
			file:    key,
		}
	})
}

// uploadFilePrompt loads local files and shows upload selection
func (m Model) uploadFilePrompt() tea.Cmd {
	return m.loadLocalFiles(".")
}

// loadLocalFiles loads files and directories from the specified path
func (m Model) loadLocalFiles(path string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		entries, err := os.ReadDir(path)
		if err != nil {
			return localFilesLoadedMsg{err: err}
		}

				var localItems []LocalItem
		
		// Always add parent directory entry (allows going above starting directory)
		localItems = append(localItems, LocalItem{
			Name:  "..",
			IsDir: true,
			Size:  0,
		})

		// Add directories and files (excluding hidden ones)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue // Skip hidden files/directories
			}

			info, err := entry.Info()
			if err != nil {
				continue
			}

			localItems = append(localItems, LocalItem{
				Name:  entry.Name(),
				IsDir: entry.IsDir(),
				Size:  info.Size(),
			})
		}

		// Sort: directories first, then files
		sort.Slice(localItems, func(i, j int) bool {
			if localItems[i].IsDir != localItems[j].IsDir {
				return localItems[i].IsDir
			}
			return localItems[i].Name < localItems[j].Name
		})

		return localFilesLoadedMsg{items: localItems, path: path}
	})
}

// uploadFile uploads a file to S3
func (m Model) uploadFile(fullPath string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return fileUploadedMsg{err: fmt.Errorf("failed to read file '%s': %w", fullPath, err)}
		}

		// Get just the filename for the S3 key
		filename := filepath.Base(fullPath)

		// Construct S3 key
		key := filename
		if m.currentPath != "" {
			key = m.currentPath + "/" + filename
		}

		err = m.s3Client.PutObject(context.Background(), m.bucket, key, data)
		if err != nil {
			return fileUploadedMsg{err: err}
		}

		return fileUploadedMsg{filename: filename}
	})
}

// deleteFile deletes a file from S3
func (m Model) deleteFile(key string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		err := m.s3Client.DeleteObject(context.Background(), m.bucket, key)
		if err != nil {
			return fileDeletedMsg{err: err}
		}

		// Get just the filename for display
		filename := filepath.Base(key)
		return fileDeletedMsg{filename: filename}
	})
}

// downloadFile downloads a file from S3 to local directory
func (m Model) downloadFile(key string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		data, err := m.s3Client.GetObject(context.Background(), m.bucket, key)
		if err != nil {
			return fileDownloadedMsg{err: err}
		}

		// Get just the filename from the key
		filename := filepath.Base(key)

		// Write to local file
		err = os.WriteFile(filename, data, 0644)
		if err != nil {
			return fileDownloadedMsg{err: fmt.Errorf("failed to write file '%s': %w", filename, err)}
		}

		return fileDownloadedMsg{filename: filename}
	})
}

// calculatePreviewWidth calculates the optimal width for the preview window
func (m Model) calculatePreviewWidth() int {
	if len(m.previewLines) == 0 {
		return 80 // Default width
	}

	maxLineLength := 0
	for _, line := range m.previewLines {
		// Use rune count for proper Unicode handling, account for line numbers (4 digits + " │ ")
		lineLength := utf8.RuneCountInString(line) + 6
		if lineLength > maxLineLength {
			maxLineLength = lineLength
		}
	}

	// Add padding for borders and content padding (4 chars for borders + 4 for padding)
	optimalWidth := maxLineLength + 8

	// Limit to terminal width minus some margin
	maxAllowedWidth := m.width - 10
	if maxAllowedWidth < 40 {
		maxAllowedWidth = 40 // Minimum usable width
	}

	if optimalWidth > maxAllowedWidth {
		return maxAllowedWidth
	}

	// Ensure minimum width for readability
	if optimalWidth < 60 {
		return 60
	}

	return optimalWidth
}

// formatSize formats file size in human-readable format
func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}
