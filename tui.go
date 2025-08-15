package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	ViewRename
	ViewConfirm
)

// LocalItem represents a local file or directory
type LocalItem struct {
	Name  string
	IsDir bool
	Size  int64
}

// DirStats holds cached directory statistics
type DirStats struct {
	Size         int64
	LastModified string
	SizeTimeout  bool
	DateTimeout  bool
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
	yankedFiles     []string // Keys of files that have been yanked for copying
	renameInput     string   // Current input for renaming
	renameOriginal  string   // Original filename being renamed
	renameCursor    int      // Cursor position in rename input
	scrollOffset    int      // Current scroll offset for file list
	confirmAction   string   // Action being confirmed (delete, download, upload)
	confirmTarget   string   // Target file/path for confirmation
	confirmData     interface{} // Additional data for confirmation action
	dirStatsCache   map[string]DirStats // Cache for directory statistics
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

type fileCopiedMsg struct {
	sourceKey string
	destKey   string
	err       error
}

type fileRenamedMsg struct {
	oldKey string
	newKey string
	err    error
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

type dirStatsMsg struct {
	dirKey       string
	size         int64
	lastModified string
	sizeTimeout  bool
	dateTimeout  bool
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
		s3Client:      s3Client,
		bucket:        bucket,
		currentPath:   "",
		objects:       []S3Object{},
		cursor:        0,
		viewMode:      ViewBrowser,
		loading:       true,
		dirStatsCache: make(map[string]DirStats),
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
		case ViewRename:
			return m.updateRename(msg)
		case ViewConfirm:
			return m.updateConfirm(msg)
		}

	case objectsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.objects = msg.objects
			m.cursor = 0
			m.scrollOffset = 0
			m.err = nil
			
			// Trigger directory stats calculations for directories that don't have cached stats
			var cmds []tea.Cmd
			for _, obj := range m.objects {
				if obj.IsDir {
					if _, exists := m.dirStatsCache[obj.Key]; !exists {
						cmds = append(cmds, m.calculateDirStats(obj.Key))
					}
				}
			}
			if len(cmds) > 0 {
				return m, tea.Batch(cmds...)
			}
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

	case fileCopiedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.statusMessage = ""
		} else {
			m.err = nil
			// Handle both single file and multiple file copy messages
			if strings.Contains(msg.sourceKey, "files") {
				// Multiple files copied
				m.statusMessage = fmt.Sprintf("✓ Copied %s successfully", msg.sourceKey)
			} else {
				// Single file copied (legacy support)
				sourceFilename := filepath.Base(msg.sourceKey)
				destFilename := filepath.Base(msg.destKey)
				m.statusMessage = fmt.Sprintf("✓ Copied '%s' to '%s' successfully", sourceFilename, destFilename)
			}
			// Refresh the directory to show the new file(s)
			return m, m.loadObjects()
		}
		return m, nil

	case fileRenamedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.statusMessage = ""
		} else {
			m.err = nil
			oldFilename := filepath.Base(msg.oldKey)
			newFilename := filepath.Base(msg.newKey)
			m.statusMessage = fmt.Sprintf("✓ Renamed '%s' to '%s' successfully", oldFilename, newFilename)
			// Refresh the directory to show the renamed file
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

	case dirStatsMsg:
		// Update directory statistics cache
		stats := DirStats{
			Size:         msg.size,
			LastModified: msg.lastModified,
			SizeTimeout:  msg.sizeTimeout,
			DateTimeout:  msg.dateTimeout,
		}
		m.dirStatsCache[msg.dirKey] = stats
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
			m.updateScroll()
		}

	case "down", "j":
		if m.cursor < len(m.objects)-1 {
			m.cursor++
			m.updateScroll()
		}

	case "enter", "l", "o":
		if len(m.objects) > 0 {
			selected := m.objects[m.cursor]
			if selected.IsDir {
				// Navigate into directory
				m.currentPath = selected.Key
				m.loading = true
				// Clear directory stats cache when navigating to ensure fresh calculations
				m.dirStatsCache = make(map[string]DirStats)
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
			// Clear directory stats cache when navigating to ensure fresh calculations
			m.dirStatsCache = make(map[string]DirStats)
			return m, m.loadObjects()
		}

	case "r":
		// Rename selected file
		if len(m.objects) > 0 {
			selected := m.objects[m.cursor]
			if !selected.IsDir {
				m.renameOriginal = selected.Key
				m.renameInput = filepath.Base(selected.Key)
				m.renameCursor = len(m.renameInput) // Set cursor at end
				m.viewMode = ViewRename
				m.err = nil
				m.statusMessage = ""
			}
		}

	case "d":
		// Download selected file (with confirmation)
		if len(m.objects) > 0 {
			selected := m.objects[m.cursor]
			if !selected.IsDir {
				m.confirmAction = "download"
				m.confirmTarget = selected.Key
				m.viewMode = ViewConfirm
				m.err = nil
				m.statusMessage = ""
			}
		}

	case "u":
		// Upload file from current directory
		return m, m.uploadFilePrompt()

	case "x":
		// Delete selected file (with confirmation)
		if len(m.objects) > 0 {
			selected := m.objects[m.cursor]
			if !selected.IsDir {
				m.confirmAction = "delete"
				m.confirmTarget = selected.Key
				m.viewMode = ViewConfirm
				m.err = nil
				m.statusMessage = ""
			}
		}

	case "y":
		// Yank (mark for copying) selected file - toggle behavior
		if len(m.objects) > 0 {
			selected := m.objects[m.cursor]
			if !selected.IsDir {
				// Check if file is already yanked
				isYanked := false
				yankedIndex := -1
				for i, yankedKey := range m.yankedFiles {
					if yankedKey == selected.Key {
						isYanked = true
						yankedIndex = i
						break
					}
				}

				if isYanked {
					// Remove from yanked files
					m.yankedFiles = append(m.yankedFiles[:yankedIndex], m.yankedFiles[yankedIndex+1:]...)
				} else {
					// Add to yanked files
					m.yankedFiles = append(m.yankedFiles, selected.Key)
				}
				m.err = nil

				// Move cursor to next item
				if m.cursor < len(m.objects)-1 {
					m.cursor++
					m.updateScroll()
				}
			}
		}

	case "p":
		// Paste yanked files to current location
		if len(m.yankedFiles) > 0 {
			return m, m.pasteFiles()
		}

	case "c":
		// Clear all yanked files
		if len(m.yankedFiles) > 0 {
			count := len(m.yankedFiles)
			m.yankedFiles = []string{}
			m.statusMessage = fmt.Sprintf("✓ Cleared %d yanked file(s)", count)
			m.err = nil
		}

	case "g":
		// Go to first item
		if len(m.objects) > 0 {
			m.cursor = 0
			m.updateScroll()
		}

	case "G":
		// Go to last item
		if len(m.objects) > 0 {
			m.cursor = len(m.objects) - 1
			m.updateScroll()
		}

	case "ctrl+d":
		// Page down (half screen)
		if len(m.objects) > 0 {
			availableHeight := m.height - 8
			if availableHeight < 5 {
				availableHeight = 5
			}
			pageSize := availableHeight / 2
			if pageSize < 1 {
				pageSize = 1
			}

			m.cursor += pageSize
			if m.cursor >= len(m.objects) {
				m.cursor = len(m.objects) - 1
			}
			m.updateScroll()
		}

	case "ctrl+u":
		// Page up (half screen)
		if len(m.objects) > 0 {
			availableHeight := m.height - 8
			if availableHeight < 5 {
				availableHeight = 5
			}
			pageSize := availableHeight / 2
			if pageSize < 1 {
				pageSize = 1
			}

			m.cursor -= pageSize
			if m.cursor < 0 {
				m.cursor = 0
			}
			m.updateScroll()
		}

	case "?":
		m.viewMode = ViewHelp
	}

	return m, nil
}

// updateScroll adjusts scroll offset based on cursor position and screen size
func (m *Model) updateScroll() {
	if len(m.objects) == 0 {
		m.scrollOffset = 0
		return
	}

	// Calculate available height for file list
	// Account for: title (2 lines), status/error (2 lines), help (2 lines), borders/padding
	availableHeight := m.height - 8
	if availableHeight < 5 {
		availableHeight = 5 // Minimum reasonable height
	}

	scrollback := 2

	// Scroll down if cursor is too close to bottom
	if m.cursor >= m.scrollOffset+availableHeight-scrollback {
		m.scrollOffset = m.cursor - availableHeight + scrollback + 1
	}

	// Scroll up if cursor is too close to top
	if m.cursor < m.scrollOffset+scrollback {
		m.scrollOffset = m.cursor - scrollback
	}

	// Ensure scroll offset stays within bounds
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
	if m.scrollOffset > len(m.objects)-availableHeight {
		m.scrollOffset = len(m.objects) - availableHeight
		if m.scrollOffset < 0 {
			m.scrollOffset = 0
		}
	}
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
				// Upload file (with confirmation)
				fullPath := filepath.Join(m.localPath, selected.Name)
				m.confirmAction = "upload"
				m.confirmTarget = selected.Name
				m.confirmData = fullPath
				m.viewMode = ViewConfirm
				m.err = nil
				m.statusMessage = ""
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

// updateRename handles rename view updates
func (m Model) updateRename(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		// Cancel rename
		m.viewMode = ViewBrowser
		m.renameInput = ""
		m.renameOriginal = ""
		m.renameCursor = 0
		return m, nil
	case "enter":
		// Confirm rename
		if m.renameInput != "" && m.renameInput != filepath.Base(m.renameOriginal) {
			m.viewMode = ViewBrowser
			m.loading = true
			cmd := m.renameFile(m.renameOriginal, m.renameInput)
			m.renameInput = ""
			m.renameOriginal = ""
			m.renameCursor = 0
			return m, cmd
		}
		// If no change, just go back to browser
		m.viewMode = ViewBrowser
		m.renameInput = ""
		m.renameOriginal = ""
		m.renameCursor = 0
		return m, nil
	case "backspace":
		// Remove character to the left of cursor
		if m.renameCursor > 0 && len(m.renameInput) > 0 {
			m.renameInput = m.renameInput[:m.renameCursor-1] + m.renameInput[m.renameCursor:]
			m.renameCursor--
		}
	case "delete":
		// Remove character at cursor position
		if m.renameCursor < len(m.renameInput) {
			m.renameInput = m.renameInput[:m.renameCursor] + m.renameInput[m.renameCursor+1:]
		}
	case "left":
		// Move cursor left
		if m.renameCursor > 0 {
			m.renameCursor--
		}
	case "right":
		// Move cursor right
		if m.renameCursor < len(m.renameInput) {
			m.renameCursor++
		}
	case "home", "ctrl+a":
		// Go to beginning
		m.renameCursor = 0
	case "end", "ctrl+e":
		// Go to end
		m.renameCursor = len(m.renameInput)
	case "ctrl+u":
		// Delete all text to the left of cursor
		m.renameInput = m.renameInput[m.renameCursor:]
		m.renameCursor = 0
	case "ctrl+w":
		// Delete word to the left of cursor
		if m.renameCursor > 0 {
			// Find the start of the current word
			start := m.renameCursor - 1
			// Skip any trailing spaces
			for start >= 0 && m.renameInput[start] == ' ' {
				start--
			}
			// Find the beginning of the word
			for start >= 0 && m.renameInput[start] != ' ' {
				start--
			}
			start++ // Move to the first character of the word

			// Delete from start to cursor
			m.renameInput = m.renameInput[:start] + m.renameInput[m.renameCursor:]
			m.renameCursor = start
		}
	default:
		// Add character to input (only printable characters)
		if len(msg.String()) == 1 && msg.String()[0] >= 32 && msg.String()[0] <= 126 {
			// Insert character at cursor position
			m.renameInput = m.renameInput[:m.renameCursor] + msg.String() + m.renameInput[m.renameCursor:]
			m.renameCursor++
		}
	}

	// Ensure cursor stays within bounds
	if m.renameCursor < 0 {
		m.renameCursor = 0
	}
	if m.renameCursor > len(m.renameInput) {
		m.renameCursor = len(m.renameInput)
	}

	return m, nil
}

// updateConfirm handles confirmation view updates
func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc", "n", "N":
		// Cancel confirmation
		m.viewMode = ViewBrowser
		m.confirmAction = ""
		m.confirmTarget = ""
		m.confirmData = nil
		return m, nil
	case "y", "Y", "enter":
		// Confirm action
		m.viewMode = ViewBrowser
		m.loading = true
		
		var cmd tea.Cmd
		switch m.confirmAction {
		case "delete":
			cmd = m.deleteFile(m.confirmTarget)
		case "download":
			cmd = m.downloadFile(m.confirmTarget)
		case "upload":
			if fullPath, ok := m.confirmData.(string); ok {
				cmd = m.uploadFile(fullPath)
			}
		}
		
		// Clear confirmation state
		m.confirmAction = ""
		m.confirmTarget = ""
		m.confirmData = nil
		
		return m, cmd
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
	case ViewRename:
		return m.viewRename()
	case ViewConfirm:
		return m.viewConfirm()
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
	if len(m.yankedFiles) > 0 {
		title += fmt.Sprintf(" | Yanked: %d file(s)", len(m.yankedFiles))
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
			// Update scroll position
			m.updateScroll()

			// Calculate available height for file list
			availableHeight := m.height - 8
			if availableHeight < 5 {
				availableHeight = 5
			}

			// Calculate visible range
			startIdx := m.scrollOffset
			endIdx := startIdx + availableHeight
			if endIdx > len(m.objects) {
				endIdx = len(m.objects)
			}

			// Calculate dynamic filename width based on terminal width
			maxSizeWidth := 8  // constant width for size column
			dateWidth := 19    // constant width for date column (YYYY-MM-DD HH:MM:SS)
			
			// Calculate available space for filename column
			// Account for: cursor (2), yank indicator (2), spaces between columns (6), size column (8), date column (19)
			usedWidth := 2 + 2 + 6 + maxSizeWidth + dateWidth
			availableWidth := m.width - usedWidth - 10 // Extra margin for borders and centering
			
			// Set reasonable bounds for filename width
			maxNameWidth := availableWidth
			if maxNameWidth < 15 {
				maxNameWidth = 15 // Minimum usable width
			}
			if maxNameWidth > 60 {
				maxNameWidth = 60 // Maximum to prevent overly wide tables
			}

			// Display visible items
			for i := startIdx; i < endIdx; i++ {
				obj := m.objects[i]
				cursor := " "
				if i == m.cursor {
					cursor = ">"
				}

				name := filepath.Base(obj.Key)
				if obj.IsDir {
					name += "/"
				}
				
				// Always truncate name to fit dynamic width
				displayName := name
				if len(name) > maxNameWidth {
					displayName = name[:maxNameWidth-3] + "..."
				}

				// Check if file is yanked (directories can't be yanked)
				// Always reserve space for yank indicator to maintain consistent alignment
				yankedIndicator := " " // Default: empty space
				if !obj.IsDir {
					for _, yankedKey := range m.yankedFiles {
						if obj.Key == yankedKey {
							yankedIndicator = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffff00")).Render("●")
							break
						}
					}
				}

				// Format with consistent column alignment for both files and directories
				paddedName := fmt.Sprintf("%-*s", maxNameWidth, displayName)
				
				var paddedSize string
				var displayDate string
				
				if obj.IsDir {
					// Check if we have cached directory stats
					if stats, exists := m.dirStatsCache[obj.Key]; exists {
						if stats.SizeTimeout {
							paddedSize = fmt.Sprintf("%*s", maxSizeWidth, "? B")
						} else {
							size := formatSize(stats.Size)
							paddedSize = fmt.Sprintf("%*s", maxSizeWidth, size)
						}
						
						if stats.DateTimeout {
							displayDate = "N/A"
						} else {
							displayDate = stats.LastModified
						}
					} else {
						// No cached stats, show placeholders and trigger calculation
						paddedSize = fmt.Sprintf("%*s", maxSizeWidth, "...")
						displayDate = "..."
						// Note: We'll trigger calculation after the render loop
					}
				} else {
					size := formatSize(obj.Size)
					paddedSize = fmt.Sprintf("%*s", maxSizeWidth, size)
					displayDate = obj.LastModified
				}

				// Apply styling based on type
				var styledName string
				if obj.IsDir {
					styledName = directoryStyle.Render(paddedName)
				} else {
					styledName = fileStyle.Render(paddedName)
				}

				// Use consistent format for all items (always has yank indicator space reserved)
				line := fmt.Sprintf("%s %s %s %s %s", cursor, yankedIndicator, styledName, paddedSize, displayDate)

				if i == m.cursor {
					line = selectedStyle.Render(line)
				}

				s.WriteString(line)
				s.WriteString("\n")
			}

			// Add scroll indicators if needed
			if len(m.objects) > availableHeight {
				scrollInfo := fmt.Sprintf("(%d-%d of %d)", startIdx+1, endIdx, len(m.objects))
				s.WriteString(helpStyle.Render(scrollInfo))
				s.WriteString("\n")
			}
		}
	}

	// Help text
	s.WriteString("\n")
	s.WriteString(helpStyle.Render("?: help"))

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
  ctrl+u      Page up (half screen)
  ctrl+d      Page down (half screen)
  g           Go to first item
  G           Go to last item
  ←/h         Go back to parent directory
  →/l/o/enter Enter directory or preview file

Actions:
  ?           Show this help
  q/ctrl+c    Quit application

File Operations:
  enter/l/o   Preview text files or enter directories
  d           Download selected file to current directory
  u           Upload file from current directory
  x           Delete selected file from S3
  y           Yank (mark) selected file for copying (toggle)
  p           Paste all yanked files to current location
  c           Clear all yanked files
  r           Rename selected file

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
  - Copy/paste files within the bucket
  - Rename files with interactive popup
  - Shows file sizes and modification dates
  - Distinguishes directories from files
  - Automatic name conflict resolution (adds _copy_N suffix)

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

// viewRename renders the rename popup view
func (m Model) viewRename() string {
	var s strings.Builder

	title := fmt.Sprintf("Rename: %s", filepath.Base(m.renameOriginal))
	s.WriteString(titleStyle.Render(title))
	s.WriteString("\n\n")

	// Error display
	if m.err != nil {
		s.WriteString(errorStyle.Render(fmt.Sprintf("Error: %s", m.err.Error())))
		s.WriteString("\n\n")
	}

	// Input field label
	s.WriteString("New name:")
	s.WriteString("\n")

	// Create a simple input box style
	inputStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#0066cc")).
		Padding(0, 1).
		Width(40)

	// Render input with cursor
	inputContent := m.renderInputWithCursor()

	s.WriteString(inputStyle.Render(inputContent))
	s.WriteString("\n\n")

	// Instructions
	s.WriteString(helpStyle.Render("enter: confirm • esc: cancel • ←/→: move cursor • ctrl+a/e: start/end • ctrl+u: clear left • ctrl+w: delete word"))

	// Wrap content and center it
	content := s.String()

	// Create a popup-style border
	popupStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("#0066cc")).
		Padding(2, 4).
		Align(lipgloss.Center)

	popup := popupStyle.Render(content)

	// Center the popup on screen
	if m.width > 0 && m.height > 0 {
		centered := centerStyle.Width(m.width).Render(popup)
		return verticalCenterStyle.Height(m.height).Render(centered)
	}
	return popup
}

// viewConfirm renders the confirmation popup view
func (m Model) viewConfirm() string {
	var s strings.Builder

	// Create title based on action
	var title, message string
	filename := filepath.Base(m.confirmTarget)
	
	switch m.confirmAction {
	case "delete":
		title = "Confirm Delete"
		message = fmt.Sprintf("Are you sure you want to delete '%s'?\n\nThis action cannot be undone.", filename)
	case "download":
		title = "Confirm Download"
		message = fmt.Sprintf("Download '%s' to current directory?", filename)
	case "upload":
		title = "Confirm Upload"
		if m.currentPath != "" {
			message = fmt.Sprintf("Upload '%s' to S3 path '/%s'?", filename, m.currentPath)
		} else {
			message = fmt.Sprintf("Upload '%s' to S3 root?", filename)
		}
	default:
		title = "Confirm Action"
		message = "Are you sure?"
	}

	s.WriteString(titleStyle.Render(title))
	s.WriteString("\n\n")

	// Error display
	if m.err != nil {
		s.WriteString(errorStyle.Render(fmt.Sprintf("Error: %s", m.err.Error())))
		s.WriteString("\n\n")
	}

	// Message
	s.WriteString(message)
	s.WriteString("\n\n")

	// Instructions
	s.WriteString(helpStyle.Render("y/enter: yes • n/esc: no"))

	// Wrap content and center it
	content := s.String()
	
	// Create a popup-style border (orange color for attention)
	popupStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("#cc6600")).
		Padding(2, 4).
		Align(lipgloss.Center)
	
	popup := popupStyle.Render(content)

	// Center the popup on screen
	if m.width > 0 && m.height > 0 {
		centered := centerStyle.Width(m.width).Render(popup)
		return verticalCenterStyle.Height(m.height).Render(centered)
	}
	return popup
}

// renderInputWithCursor renders the input text with a visible cursor
func (m Model) renderInputWithCursor() string {
	if len(m.renameInput) == 0 {
		// Empty input, show cursor at beginning
		return "█"
	}

	// Insert cursor character at cursor position
	before := m.renameInput[:m.renameCursor]
	after := m.renameInput[m.renameCursor:]

	// Use a block cursor character
	cursor := "█"

	// If cursor is at the end, append cursor
	if m.renameCursor >= len(m.renameInput) {
		return m.renameInput + cursor
	}

	// If cursor is in the middle, replace the character at cursor position with highlighted version
	if m.renameCursor < len(m.renameInput) {
		// Create a highlighted version of the character under cursor
		charUnderCursor := string(m.renameInput[m.renameCursor])
		highlightedChar := lipgloss.NewStyle().
			Background(lipgloss.Color("#ffffff")).
			Foreground(lipgloss.Color("#000000")).
			Render(charUnderCursor)

		return before + highlightedChar + after[1:]
	}

	return before + cursor + after
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

// renameFile renames a file in S3
func (m Model) renameFile(oldKey, newFilename string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		// Construct the new key with the same path but new filename
		var newKey string
		if strings.Contains(oldKey, "/") {
			// File is in a subdirectory
			parts := strings.Split(oldKey, "/")
			parts[len(parts)-1] = newFilename // Replace the filename
			newKey = strings.Join(parts, "/")
		} else {
			// File is in root
			newKey = newFilename
		}

		// Check if the new name already exists
		for _, obj := range m.objects {
			if obj.Key == newKey {
				return fileRenamedMsg{err: fmt.Errorf("file '%s' already exists", newFilename)}
			}
		}

		// Perform the rename operation (copy + delete)
		err := m.s3Client.RenameObject(context.Background(), m.bucket, oldKey, newKey)
		if err != nil {
			return fileRenamedMsg{err: err}
		}

		// Update yanked files references if the renamed file was yanked
		for i, yankedKey := range m.yankedFiles {
			if yankedKey == oldKey {
				m.yankedFiles[i] = newKey
				break
			}
		}

		return fileRenamedMsg{
			oldKey: oldKey,
			newKey: newKey,
		}
	})
}

// pasteFiles copies all yanked files to the current location
func (m Model) pasteFiles() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		if len(m.yankedFiles) == 0 {
			return fileCopiedMsg{err: fmt.Errorf("no files yanked for copying")}
		}

		var copiedFiles []string
		var errors []string

		for _, yankedFile := range m.yankedFiles {
			// Get the filename from the yanked file
			filename := filepath.Base(yankedFile)

			// Construct destination key
			var destKey string
			if m.currentPath != "" {
				destKey = m.currentPath + "/" + filename
			} else {
				destKey = filename
			}

			// Check if file already exists in current location
			for _, obj := range m.objects {
				if obj.Key == destKey {
					// File exists, create a new name with suffix
					ext := filepath.Ext(filename)
					nameWithoutExt := strings.TrimSuffix(filename, ext)

					// Find a unique name by adding numbers
					counter := 1
					for {
						newFilename := fmt.Sprintf("%s_copy_%d%s", nameWithoutExt, counter, ext)
						if m.currentPath != "" {
							destKey = m.currentPath + "/" + newFilename
						} else {
							destKey = newFilename
						}

						// Check if this new name exists
						exists := false
						for _, existingObj := range m.objects {
							if existingObj.Key == destKey {
								exists = true
								break
							}
						}
						if !exists {
							break
						}
						counter++
					}
					break
				}
			}

			// Perform the copy operation
			err := m.s3Client.CopyObject(context.Background(), m.bucket, yankedFile, destKey)
			if err != nil {
				errors = append(errors, fmt.Sprintf("%s: %v", filepath.Base(yankedFile), err))
			} else {
				copiedFiles = append(copiedFiles, filepath.Base(destKey))
			}
		}

		// Return result with summary
		if len(errors) > 0 {
			errorMsg := fmt.Sprintf("Failed to copy %d file(s): %s", len(errors), strings.Join(errors, ", "))
			if len(copiedFiles) > 0 {
				errorMsg += fmt.Sprintf(". Successfully copied: %s", strings.Join(copiedFiles, ", "))
			}
			return fileCopiedMsg{err: fmt.Errorf(errorMsg)}
		}

		return fileCopiedMsg{
			sourceKey: fmt.Sprintf("%d files", len(m.yankedFiles)),
			destKey:   strings.Join(copiedFiles, ", "),
		}
	})
}

// calculateDirStats calculates directory statistics with timeouts
func (m Model) calculateDirStats(dirKey string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		prefix := dirKey
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}

		// Channel for size calculation
		sizeChan := make(chan int64, 1)
		sizeErrChan := make(chan error, 1)
		
		// Channel for last modified calculation
		dateChan := make(chan string, 1)
		dateErrChan := make(chan error, 1)

		// Start size calculation goroutine
		go func() {
			objects, err := m.s3Client.ListObjects(context.Background(), m.bucket, prefix)
			if err != nil {
				sizeErrChan <- err
				return
			}

			var totalSize int64
			for _, obj := range objects {
				if !obj.IsDir {
					totalSize += obj.Size
				}
			}
			sizeChan <- totalSize
		}()

		// Start date calculation goroutine
		go func() {
			objects, err := m.s3Client.ListObjects(context.Background(), m.bucket, prefix)
			if err != nil {
				dateErrChan <- err
				return
			}

			var latestDate string
			for _, obj := range objects {
				if !obj.IsDir && (latestDate == "" || obj.LastModified > latestDate) {
					latestDate = obj.LastModified
				}
			}
			if latestDate == "" {
				latestDate = "N/A"
			}
			dateChan <- latestDate
		}()

		// Wait for results with timeouts
		var size int64 = 0
		var lastModified string = "N/A"
		var sizeTimeout bool = false
		var dateTimeout bool = false

		// Wait for size with 1-second timeout
		select {
		case size = <-sizeChan:
		case <-sizeErrChan:
			sizeTimeout = true
		case <-time.After(1 * time.Second):
			sizeTimeout = true
		}

		// Wait for date with 2-second timeout
		select {
		case lastModified = <-dateChan:
		case <-dateErrChan:
			dateTimeout = true
		case <-time.After(2 * time.Second):
			dateTimeout = true
		}

		return dirStatsMsg{
			dirKey:       dirKey,
			size:         size,
			lastModified: lastModified,
			sizeTimeout:  sizeTimeout,
			dateTimeout:  dateTimeout,
		}
	})
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
