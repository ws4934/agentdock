// Package desktop 提供内置 Computer Use；平台调用只在 native backend 中发生。
package desktop

import "context"

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}
type Display struct {
	ID          uint32 `json:"id"`
	Bounds      Rect   `json:"bounds"`
	PixelWidth  int    `json:"pixel_width"`
	PixelHeight int    `json:"pixel_height"`
	Main        bool   `json:"main"`
}
type Window struct {
	ID     uint32 `json:"id"`
	PID    int    `json:"pid"`
	Title  string `json:"title"`
	Bounds Rect   `json:"bounds"`
}
type Application struct {
	Path     string `json:"app_path,omitempty"`
	PID      int    `json:"pid"`
	Name     string `json:"name"`
	BundleID string `json:"bundle_id"`
}
type State struct {
	WindowsTruncated bool          `json:"windows_truncated"`
	Cursor           Point         `json:"cursor"`
	FrontmostPID     int           `json:"frontmost_pid"`
	Displays         []Display     `json:"displays"`
	Windows          []Window      `json:"windows"`
	Applications     []Application `json:"applications"`
}
type Permissions struct {
	ScreenRecording bool `json:"screen_recording"`
	Accessibility   bool `json:"accessibility"`
	SecureInput     bool `json:"secure_input"`
}
type Element struct {
	EnabledKnown  bool   `json:"enabled_known"`
	ID            string `json:"id"`
	Role          string `json:"role"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Bounds        Rect   `json:"bounds"`
	Enabled       bool   `json:"enabled"`
	Pressable     bool   `json:"pressable"`
	ValueSettable bool   `json:"value_settable"`
	// Path 只保存在进程内，不作为可由调用方伪造的 AX 地址公开。
	Path []int `json:"path,omitempty"`
}
type Tree struct {
	Elements  []Element `json:"elements"`
	Truncated bool      `json:"truncated"`
}
type Capture struct {
	Data   []byte
	Width  int
	Height int
}

// Backend 的输入已由 Service 校验；实现仍须在发送事件前重新核验前台 PID。
// 所有系统调用必须有界；不得持有键盘或鼠标按下状态跨越工具调用。
type Backend interface {
	Supported() bool
	Permissions() Permissions
	RequestPermission(string) error
	State(context.Context) (State, error)
	Capture(context.Context, Display, int) (Capture, error)
	Tree(context.Context, int, int, int) (Tree, error)
	Press(context.Context, int, Element) error
	Activate(context.Context, int) error
	Mouse(context.Context, int, string, Point, int, int, uint64) error
	Scroll(context.Context, int, int, int) error
	Key(context.Context, int, uint16, uint64) error
	Text(context.Context, int, []uint16) error
}

type SnapshotRequest struct {
	TaskID        string `json:"task_id,omitempty"`
	Mode          string `json:"mode,omitempty"`
	WindowID      uint32 `json:"window_id,omitempty"`
	PID           int    `json:"pid,omitempty"`
	DisplayID     uint32 `json:"display_id,omitempty"`
	Screenshot    *bool  `json:"screenshot,omitempty"`
	Accessibility bool   `json:"accessibility,omitempty"`
	MaxDimension  int    `json:"max_dimension,omitempty"`
	MaxNodes      int    `json:"max_nodes,omitempty"`
	MaxDepth      int    `json:"max_depth,omitempty"`
}
type PermissionRequest struct {
	TaskID     string `json:"task_id,omitempty"`
	Permission string `json:"permission"`
}
type ActionRequest struct {
	TaskID     string   `json:"task_id,omitempty"`
	Action     string   `json:"action"`
	SnapshotID string   `json:"snapshot_id"`
	PID        int      `json:"pid,omitempty"`
	Point      *Point   `json:"point,omitempty"`
	Space      string   `json:"space,omitempty"`
	ElementID  string   `json:"element_id,omitempty"`
	Button     string   `json:"button,omitempty"`
	ClickCount int      `json:"click_count,omitempty"`
	Path       []Point  `json:"path,omitempty"`
	DurationMS int      `json:"duration_ms,omitempty"`
	DeltaX     int      `json:"delta_x,omitempty"`
	DeltaY     int      `json:"delta_y,omitempty"`
	Key        string   `json:"key,omitempty"`
	Modifiers  []string `json:"modifiers,omitempty"`
	Text       string   `json:"text,omitempty"`
}
