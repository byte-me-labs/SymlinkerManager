package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"symlinkermanager/internal/apply"
	"symlinkermanager/internal/apppath"
	"symlinkermanager/internal/config"
	"symlinkermanager/internal/elevate"
	"symlinkermanager/internal/job"
	"symlinkermanager/internal/pathutil"
	"symlinkermanager/internal/scan"
	"symlinkermanager/internal/winlink"
)

type junctionInspector struct{}

func (junctionInspector) Inspect(path string) (scan.TargetInfo, error) {
	return winlink.Inspect(path)
}

type junctionLinker struct{}

func (junctionLinker) Create(source, target string) error { return winlink.Create(source, target) }
func (junctionLinker) Remove(path string) error           { return winlink.Remove(path) }

// App is the window's state. The view reads it on the main thread; background
// work changes it through Window.Update.
type App struct {
	win     *mygo.Window
	scanner *scan.Scanner

	cfgPath  string
	cfg      *config.Config
	readOnly bool
	cfgError string

	results  []scan.Result
	checked  []bool
	intents  map[int]apply.Intent
	outcomes map[int]apply.Outcome
	failed   []apply.Request

	// The source path's fix toggle binds to this flag; intents stay the
	// truth, and each build syncs the flag before drawing the toggle.
	createFlags map[int]*bool

	list     ui.ListState
	scanID   int
	busy     bool
	scanning bool
	status   string

	// Edit dialog.
	modalOpen    bool
	editingIndex int
	editName     string
	editSource   string
	editTarget   string
	editError    string
	editSession  int

	// Confirmations.
	confirmRemoveLink bool
	removeLinkIndex   int
	confirmDelete     bool
	deleteIndex       int
	confirmRename     bool
	renameIndex       int
}

func newApp() *App {
	return &App{
		editingIndex: -1,
		intents:      make(map[int]apply.Intent),
		outcomes:     make(map[int]apply.Outcome),
		createFlags:  make(map[int]*bool),
	}
}

// start loads the config and creates the window. It runs on the main thread.
func (a *App) start() {
	a.scanner = scan.New(junctionInspector{})

	path, err := apppath.ConfigPath()
	switch {
	case err != nil:
		a.cfg = config.New()
		a.readOnly = true
		a.cfgError = err.Error()
		a.status = "无法确定配置文件位置，已进入只读模式"
	default:
		a.cfgPath = path
		cfg, created, loadErr := config.LoadOrCreate(path)
		switch {
		case loadErr != nil:
			a.cfg = config.New()
			a.readOnly = true
			a.cfgError = loadErr.Error()
			a.status = "配置文件损坏，已进入只读模式；原文件不会被覆盖"
		case cfg.Version != config.CurrentVersion:
			a.cfg = cfg
			a.readOnly = true
			a.cfgError = fmt.Sprintf("配置版本 %d 不受支持（期望 %d）", cfg.Version, config.CurrentVersion)
			a.status = "配置版本不受支持，已进入只读模式"
		default:
			a.cfg = cfg
			if created {
				a.status = "已创建空配置，请点击“新建条目”开始"
			}
		}
	}
	a.resetRows()

	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:     "目录链接管理器",
		Width:     1420,
		Height:    860,
		MinWidth:  1180,
		MinHeight: 640,
		Content:   ui.View(a.view),
	})
	a.win.SetIcon(appIconPNG)
	a.refresh()
}

func (a *App) activate() {
	if a.win == nil {
		return
	}
	a.win.Restore()
	a.win.Focus()
}

// resetRows forgets scan results and per-row state after the config changes.
func (a *App) resetRows() {
	n := len(a.cfg.Links)
	a.results = nil
	a.checked = make([]bool, n)
	a.intents = make(map[int]apply.Intent, n)
	a.outcomes = make(map[int]apply.Outcome, n)
	a.failed = nil
}

func cloneConfig(c *config.Config) *config.Config {
	links := make([]config.Link, len(c.Links))
	copy(links, c.Links)
	return &config.Config{Version: c.Version, Links: links}
}

// refresh rescans the config off the main thread. A newer scan supersedes an
// older one.
func (a *App) refresh() {
	if a.cfg == nil || a.win == nil {
		return
	}
	a.scanID++
	id := a.scanID
	a.scanning = true
	cfg := cloneConfig(a.cfg)
	win := a.win
	go func() {
		results := a.scanner.Scan(cfg)
		win.Update(func() {
			if id != a.scanID {
				return
			}
			a.scanning = false
			a.results = results
			// Checkboxes only feed "批量创建"; every scan leaves them empty so
			// nothing is applied without the user's explicit pick.
			a.checked = make([]bool, len(results))
			a.intents = make(map[int]apply.Intent, len(results))
		})
	}()
}

func (a *App) resultAt(i int) (scan.Result, bool) {
	if i < 0 || i >= len(a.results) {
		return scan.Result{}, false
	}
	return a.results[i], true
}

func (a *App) checkedCount() int {
	n := 0
	for _, ok := range a.checked {
		if ok {
			n++
		}
	}
	return n
}

func (a *App) requests() []apply.Request {
	reqs := make([]apply.Request, 0, len(a.checked))
	for i, ok := range a.checked {
		if !ok {
			continue
		}
		reqs = append(reqs, apply.Request{Index: i, Checked: true, Intent: a.intents[i]})
	}
	return reqs
}

func (a *App) selectAll(on bool) {
	for i := range a.checked {
		if !on {
			a.checked[i] = false
			continue
		}
		res, ok := a.resultAt(i)
		if !ok {
			a.checked[i] = false
			continue
		}
		switch res.Status {
		case scan.StatusOK, scan.StatusMissing, scan.StatusReplaceable,
			scan.StatusConflict, scan.StatusSourceMissing:
			a.checked[i] = true
		default:
			a.checked[i] = false
		}
	}
	if !on {
		a.intents = make(map[int]apply.Intent)
	}
}

// view builds the whole window.
func (a *App) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(16).Gap(10).Children(func() {
		ui.Row(c).Gap(12).Children(func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, "目录链接管理器").FontSize(20).Bold()
				if a.cfgPath != "" {
					ui.Text(c, "配置文件："+a.cfgPath).FontSize(11).TextColor(t.TextMuted).
						SingleLine().Tooltip(a.cfgPath)
				} else {
					ui.Text(c, "未找到配置文件位置").FontSize(11).TextColor(t.TextMuted)
				}
			})
		})

		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c.Key("new"), "新建条目").
				Disabled(a.busy || a.readOnly).
				OnClick(a.beginNew)
			ui.Button(c.Key("refresh"), "刷新").
				Disabled(a.busy).
				OnClick(a.refresh)
			ui.Button(c.Key("select-all"), "全选").
				Disabled(a.busy || a.readOnly).
				OnClick(func() { a.selectAll(true) })
			ui.Button(c.Key("select-none"), "全不选").
				Disabled(a.busy || a.readOnly).
				OnClick(func() { a.selectAll(false) })
			ui.Spacer(c)
			if a.busy || a.scanning {
				ui.Spinner(c)
			}
			ui.PrimaryButton(c.Key("apply"), fmt.Sprintf("批量创建（%d）", a.checkedCount())).
				Disabled(a.busy || a.scanning || a.readOnly || a.checkedCount() == 0).
				OnClick(a.beginApply)
		})

		if a.cfgError != "" {
			a.banner(c, t.Danger, "配置只读："+a.cfgError, "", nil)
		} else if a.readOnly {
			a.banner(c, t.Danger, "当前为只读模式，修改不会写入磁盘。", "", nil)
		}
		if !a.busy && len(a.failed) > 0 {
			if winlink.IsElevated() {
				a.banner(c, t.Danger, "部分操作失败。当前已是管理员权限，请检查路径是否被占用或访问控制设置。", "", nil)
			} else {
				a.banner(c, t.Warning, fmt.Sprintf("%d 项操作失败，可尝试以管理员身份重试。", len(a.failed)),
					"以管理员身份重试失败项", a.retryElevated)
			}
		}

		a.table(c)

		ui.Row(c).Gap(12).Children(func() {
			a.statusText(c)
			ui.Spacer(c)
			if winlink.IsElevated() {
				ui.Text(c, "管理员权限").FontSize(11).TextColor(t.TextMuted)
			} else {
				ui.Text(c, "普通权限").FontSize(11).TextColor(t.TextMuted)
			}
		})
	})

	a.editModal(c)
	a.alerts(c)
}

func (a *App) banner(c *ui.Context, color ui.Color, text, action string, fn func()) {
	t := c.Theme()
	ui.Row(c).Gap(12).Padding(8, 12).Radius(8).
		Background(color.Alpha(0.12)).Border(1, color.Alpha(0.35)).
		Children(func() {
			ui.Text(c, text).FontSize(12).TextColor(t.Text).Grow(1)
			if action != "" && fn != nil {
				ui.Button(c, action).OnClick(fn)
			}
		})
}

func (a *App) statusText(c *ui.Context) {
	t := c.Theme()
	color := t.TextMuted
	if a.status != "" && (strings.HasPrefix(a.status, "失败") || strings.Contains(a.status, "失败：")) {
		color = t.Danger
	}
	ui.Text(c, a.status).FontSize(11).TextColor(color).SingleLine().Tooltip(a.status)
}

func (a *App) table(c *ui.Context) {
	t := c.Theme()
	// The name column fits its longest name: names are short, and an equal
	// share of the window would leave it mostly empty while the paths, which
	// need the room, truncate. It shrinks to its minimum for short names and
	// stops growing at its maximum, leaving the rest to the paths.
	const nameMin, nameMax float32 = 100, 240
	nameWidth := nameMin
	for _, link := range a.cfg.Links {
		w, _ := c.MeasureText(0, ui.Span{Text: link.Name, Size: t.FontSize})
		// The cell pads its content by t.Space(2.5) on each side; the extra
		// pixel keeps rounding from ellipsizing a name that just fits.
		w += 2*t.Space(2.5) + 1
		nameWidth = max(nameWidth, min(w, nameMax))
	}

	// The check, status and action columns keep their widths; source and
	// target share the room the name leaves.
	//
	// Every column is fixed against the user's drags: a width the user gave
	// a column would push the status and action columns past the window's
	// edge, and those two must stay in view. The layout owns the widths, and
	// the room the window gives is the only thing that changes them.
	cols := []ui.TableColumn{
		{Title: "", ID: "check", Width: 44, Fixed: true},
		{Title: "名称", ID: "name", Width: nameWidth, MaxWidth: nameMax, Fixed: true},
		{Title: "源目录", ID: "source", MinWidth: 160, Fixed: true},
		{Title: "目标路径", ID: "target", MinWidth: 160, Fixed: true},
		// Just wide enough for the status labels and the common details;
		// long errors truncate, with a tooltip.
		{Title: "状态", ID: "status", Width: 128, Fixed: true},
		// 88 for the button, plus the cells' 10px padding on each side, and
		// a little slack so the button's corners never meet the edge.
		{Title: "操作", ID: "actions", Width: 112, Fixed: true, Align: ui.Center},
	}
	n := len(a.cfg.Links)
	ui.Table(c, &a.list, cols, n, func(row, col int) {
		link := a.cfg.Links[row]
		res, haveRes := a.resultAt(row)
		switch col {
		case 0:
			if row < len(a.checked) {
				ui.Checkbox(c.Key(fmt.Sprintf("check-%d", row)), &a.checked[row], "").
					Disabled(a.busy || a.readOnly)
			}
		case 1:
			ui.Text(c, link.Name).SingleLine().TextColor(t.Text).Tooltip(link.Name)
		case 2:
			source := link.Source
			if haveRes && res.Source != "" {
				source = res.Source
			} else {
				source = pathutil.ExpandEnv(source)
			}
			if haveRes && res.Status == scan.StatusSourceMissing {
				a.pathCell(c, source, func() {
					on := a.intentFlag(a.createFlags, apply.IntentCreateSource, row)
					if ui.Toggle(c, on, "创建源目录").FontSize(11).
						Height(t.Space(6)).Disabled(a.busy || a.readOnly || a.scanning).
						Tooltip("应用时在源路径创建空目录后创建链接").Changed() {
						a.toggleCreateSource(row)
					}
				})
			} else {
				a.pathText(c, source)
			}
		case 3:
			target := link.Target
			if haveRes && res.Target != "" {
				target = res.Target
			} else {
				target = pathutil.ExpandEnv(target)
			}
			if haveRes && res.Status == scan.StatusConflict {
				a.pathCell(c, target, func() {
					ui.Button(c.Key(fmt.Sprintf("rename-%d", row)), "重命名").FontSize(11).
						Height(t.Space(6)).Disabled(a.busy || a.readOnly || a.scanning).
						Tooltip("把已存在的非空目标重命名为 名称.bak-时间戳；只重命名，不创建链接").
						OnClick(func() { a.askRename(row) })
				})
			} else {
				a.pathText(c, target)
			}
		case 4:
			a.statusCell(c, row, res, haveRes)
		case 5:
			a.actionsCell(c, row, res, haveRes)
		}
	}).Grow(1)
}

func (a *App) statusCell(c *ui.Context, row int, res scan.Result, haveRes bool) {
	t := c.Theme()
	ui.Column(c).Gap(2).Children(func() {
		if !haveRes {
			ui.Text(c, "检查中…").FontSize(12).TextColor(t.TextMuted)
		} else {
			label := res.Status.Label()
			ui.Text(c, label).FontSize(12).Bold().TextColor(statusColor(t, res.Status)).SingleLine()
			if res.Message != "" {
				ui.Text(c, res.Message).FontSize(11).TextColor(t.TextMuted).
					SingleLine().Tooltip(res.Message)
			}
		}
		if o, ok := a.outcomes[row]; ok {
			text := o.Action
			color := t.Success
			if o.Failed() {
				color = t.Danger
				text = o.Action + "：" + o.Err.Error()
			} else if o.Message != "" {
				text = o.Action + "：" + o.Message
			}
			ui.Text(c, text).FontSize(11).TextColor(color).SingleLine().Tooltip(text)
		}
	})
}

// actionsCell puts every per-row command in one popover menu, so that the
// action column stays narrow and the paths get the room.
// pathText shows a path; a click opens its folder, or the nearest folder above
// it that exists. Long paths are cut in the middle, keeping the drive and the
// last folder, and the tooltip has the whole path.
func (a *App) pathText(c *ui.Context, path string) ui.Element {
	return ui.Text(c, path).SingleLine().TruncateMiddle().
		TextColor(c.Theme().TextMuted).
		Tooltip(path + "\n点击打开目录").
		Cursor(ui.CursorPointer).
		OnClick(func() { a.openPath(path) })
}

// pathCell draws a path with a fix button of its own at the cell's end: source
// paths that are missing get "创建源目录", conflict targets "重命名".
// Keeping the fix next to the path it changes makes the row's menu short.
func (a *App) pathCell(c *ui.Context, path string, button func()) {
	t := c.Theme()
	ui.Row(c).Grow(1).AlignItems(ui.Center).Gap(t.Space(1)).Children(func() {
		a.pathText(c, path).Grow(1)
		button()
	})
}

// intentFlag returns the switch a path cell's fix button binds to, synced with
// the row's intent, which stays the truth.
func (a *App) intentFlag(flags map[int]*bool, intent apply.Intent, row int) *bool {
	on := flags[row]
	if on == nil {
		on = new(bool)
		flags[row] = on
	}
	*on = a.intents[row] == intent
	return on
}

func (a *App) actionsCell(c *ui.Context, row int, res scan.Result, haveRes bool) {
	isLink := haveRes && (res.Info.Kind == scan.TargetJunction || res.Info.Kind == scan.TargetSymlink)
	_, canCreate := a.createRequest(row)
	createTip := "只处理这一条，不改变其他条目"
	switch {
	case a.readOnly:
		createTip = "只读模式下不能创建"
	case !haveRes:
		createTip = "检查完成后可用"
	case res.Status == scan.StatusOK:
		createTip = "链接已正确，无需创建"
	case res.Status == scan.StatusConflict:
		createTip = "目标已存在且非空，请先点击目标路径末尾的「重命名」"
	case res.Status == scan.StatusSourceMissing && a.intents[row] != apply.IntentCreateSource:
		createTip = "请先开启源路径末尾的「创建源目录」"
	case res.Status == scan.StatusInvalid || res.Status == scan.StatusError ||
		res.Status == scan.StatusSourceInvalid:
		createTip = "配置或检查有问题，请先修正条目"
	}

	// "更多" marks this as the row's other commands; the apply-time choices
	// keep their own switches at the paths they change.
	ui.PopoverMenuButton(c.Key(fmt.Sprintf("menu-%d", row)), "更多", func(m *ui.Menu) {
		if m.Item("创建").Tooltip(createTip).Disabled(!canCreate).Chosen() {
			a.applyOne(row)
		}
		if m.Item("编辑").Disabled(a.readOnly).Chosen() {
			a.beginEdit(row)
		}
		if m.Item("移除链接").Tooltip("只删除链接本身，不删除源目录").
			Disabled(a.readOnly || !isLink).Chosen() {
			a.askRemoveLink(row)
		}
		m.Separator()
		if m.Item("删除").Tooltip("只删除配置条目，不删除任何文件或链接").Danger().Disabled(a.readOnly).Chosen() {
			a.askDelete(row)
		}
	}).Width(88).Disabled(a.busy)
}

func statusColor(t *ui.Theme, s scan.Status) ui.Color {
	switch s {
	case scan.StatusOK:
		return t.Success
	case scan.StatusMissing:
		return t.Accent
	case scan.StatusReplaceable:
		return t.Warning
	case scan.StatusConflict, scan.StatusSourceMissing,
		scan.StatusSourceInvalid, scan.StatusInvalid, scan.StatusError:
		return t.Danger
	default:
		return t.TextMuted
	}
}

func (a *App) toggleCreateSource(row int) {
	if row < 0 || row >= len(a.checked) {
		return
	}
	if a.intents[row] == apply.IntentCreateSource {
		a.intents[row] = apply.IntentAuto
		a.checked[row] = false
		a.status = "已取消创建源目录"
		return
	}
	a.intents[row] = apply.IntentCreateSource
	a.checked[row] = true
	a.status = "应用时将创建缺失的源目录"
}

// ----- edit dialog -----

func (a *App) beginNew() {
	if a.readOnly {
		return
	}
	a.editingIndex = -1
	a.editName, a.editSource, a.editTarget = "", "", ""
	a.editError = ""
	a.editSession++
	a.modalOpen = true
}

func (a *App) beginEdit(row int) {
	if a.readOnly || row < 0 || row >= len(a.cfg.Links) {
		return
	}
	link := a.cfg.Links[row]
	a.editingIndex = row
	a.editName = link.Name
	a.editSource = link.Source
	a.editTarget = link.Target
	a.editError = ""
	a.editSession++
	a.modalOpen = true
}

func (a *App) editModal(c *ui.Context) {
	t := c.Theme()
	wasOpen := a.modalOpen
	elem := ui.Modal(c, &a.modalOpen, func() {
		title := "编辑条目"
		if a.editingIndex < 0 {
			title = "新建条目"
		}
		ui.Column(c).Width(620).Gap(12).Children(func() {
			ui.Text(c, title).FontSize(16).Bold()

			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "名称").FontSize(12).TextColor(t.TextMuted)
				ui.TextInput(c.Key("edit-name"), &a.editName).
					Placeholder("例如：Steam userdata").
					Grow(1)
			})

			a.pathField(c, "源目录（数据盘上的真实目录）", &a.editSource,
				`例如：D:\MyData\Steam\userdata`, "选择源目录")

			a.pathField(c, "目标路径（系统盘上的链接位置）", &a.editTarget,
				`支持环境变量，例如：%USERPROFILE%\AppData\Roaming\Steam\userdata`, "选择目标目录")

			if a.editError != "" {
				ui.Text(c, a.editError).FontSize(12).TextColor(t.Danger)
			}

			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("edit-cancel"), "取消").Clicked() {
					a.modalOpen = false
				}
				if ui.PrimaryButton(c.Key("edit-save"), "保存").Clicked() {
					a.saveEdit()
				}
			})
		})
	})
	if wasOpen && elem.Dismissed() {
		// Escape or a click outside closed the dialog: drop this edit session
		// so a native folder picker still open cannot write into the next one.
		a.editSession++
	}
}

func (a *App) pathField(c *ui.Context, label string, value *string, placeholder, dialogTitle string) {
	t := c.Theme()
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Children(func() {
			ui.TextInput(c, value).Placeholder(placeholder).Grow(1)
			ui.Button(c, "浏览…").OnClick(func() { a.browse(value, dialogTitle) })
		})
	})
}

func (a *App) browse(field *string, title string) {
	start := pathutil.ExpandEnv(*field)
	if !filepath.IsAbs(start) {
		start = ""
	}
	session := a.editSession
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      win,
			Title:       title,
			DefaultPath: start,
			Directory:   true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		picked := offerEnvPath(win, paths[0])
		win.Update(func() {
			if session != a.editSession {
				return
			}
			*field = picked
		})
	}()
}

// offerEnvPath asks whether a browsed path should be stored in %VAR% form
// when it lies under a well-known environment variable, so the entry keeps
// working on other computers with different user names or drives. It returns
// the path to store: the %VAR% form when accepted, otherwise the original.
func offerEnvPath(win *mygo.Window, path string) string {
	suggestion, name, ok := pathutil.SuggestEnv(path)
	if !ok {
		return path
	}
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent:  win,
		Type:    mygo.MessageQuestion,
		Title:   "使用环境变量",
		Message: "所选路径位于 %" + name + "% 下。",
		Detail: "是否保存为：\n" + suggestion +
			"\n\n环境变量会在每台电脑上展开为当前用户或系统的对应路径，便于在多台电脑间复用同一份配置。",
		Buttons:       []string{"使用环境变量", "保留完整路径"},
		DefaultButton: 0,
		CancelButton:  1,
	})
	if err != nil || res.Button != 0 {
		return path
	}
	return suggestion
}

func (a *App) saveEdit() {
	if a.readOnly {
		return
	}
	name := strings.TrimSpace(a.editName)
	source := strings.TrimSpace(a.editSource)
	target := strings.TrimSpace(a.editTarget)

	c2 := cloneConfig(a.cfg)
	link := config.Link{Name: name, Source: source, Target: target}
	index := a.editingIndex
	if index < 0 {
		c2.Links = append(c2.Links, link)
		index = len(c2.Links) - 1
	} else if index < len(c2.Links) {
		c2.Links[index] = link
	} else {
		a.editError = "条目已不存在"
		return
	}

	if msg := validateEditLink(c2, index, link); msg != "" {
		a.editError = msg
		return
	}
	if err := c2.Save(a.cfgPath); err != nil {
		a.editError = "保存失败：" + err.Error()
		return
	}

	a.cfg = c2
	a.modalOpen = false
	a.editError = ""
	a.status = "已保存：" + name
	a.resetRows()
	a.refresh()
}

// validateEditLink checks only problems that involve the edited entry, so a
// broken neighbour does not make the config impossible to repair.
func validateEditLink(cfg *config.Config, index int, link config.Link) string {
	name := strings.TrimSpace(link.Name)
	if name == "" {
		return "名称不能为空"
	}
	src, err := pathutil.Normalize(link.Source)
	if err != nil {
		return "源目录：" + err.Error()
	}
	tgt, err := pathutil.Normalize(link.Target)
	if err != nil {
		return "目标路径：" + err.Error()
	}
	switch {
	case pathutil.Equal(src, tgt):
		return "源路径与目标路径相同"
	case pathutil.IsWithin(src, tgt):
		return "源目录位于目标路径之内"
	case pathutil.IsWithin(tgt, src):
		return "目标路径位于源目录之内"
	}

	for i, other := range cfg.Links {
		if i == index {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(other.Name), name) {
			return fmt.Sprintf("名称与第 %d 条重复", i+1)
		}
		otgt, err := other.TargetPath()
		if err != nil {
			continue
		}
		if pathutil.Equal(otgt, tgt) {
			return fmt.Sprintf("目标路径与第 %d 条重复", i+1)
		}
		if pathutil.IsWithin(tgt, otgt) {
			return fmt.Sprintf("目标路径位于第 %d 条的目标路径之内", i+1)
		}
		if pathutil.IsWithin(otgt, tgt) {
			return fmt.Sprintf("目标路径包含第 %d 条的目标路径", i+1)
		}
	}
	return ""
}

// ----- row actions -----

func (a *App) askRemoveLink(row int) {
	if row < 0 || row >= len(a.cfg.Links) {
		return
	}
	a.removeLinkIndex = row
	a.confirmRemoveLink = true
}

func (a *App) doRemoveLink() {
	row := a.removeLinkIndex
	res, ok := a.resultAt(row)
	if !ok || res.Target == "" {
		a.status = "移除链接失败：找不到目标路径"
		return
	}
	target := res.Target
	a.busy = true
	a.status = "正在移除链接…"
	win := a.win
	go func() {
		err := winlink.Remove(target)
		win.Update(func() {
			a.busy = false
			if err != nil {
				a.status = "移除链接失败：" + err.Error()
			} else {
				a.status = "已移除链接：" + target
			}
			a.refresh()
		})
	}()
}

func (a *App) askRename(row int) {
	if row < 0 || row >= len(a.cfg.Links) || a.busy || a.scanning || a.readOnly {
		return
	}
	a.renameIndex = row
	a.confirmRename = true
}

// doRename renames a conflicting target aside. It does not create a link: the
// follow-up scan turns the row into a missing target, which can be created
// afterwards like any other entry.
func (a *App) doRename() {
	row := a.renameIndex
	res, ok := a.resultAt(row)
	if !ok || res.Target == "" {
		a.status = "重命名失败：找不到目标路径"
		return
	}
	target := res.Target
	a.busy = true
	a.status = "正在重命名…"
	win := a.win
	go func() {
		renamed, err := apply.Rename(target, apply.Options{})
		win.Update(func() {
			a.busy = false
			if err != nil {
				a.status = "重命名失败：" + err.Error()
			} else {
				a.status = "已重命名为 " + renamed + "，可再执行创建"
			}
			a.refresh()
		})
	}()
}

func (a *App) askDelete(row int) {
	if row < 0 || row >= len(a.cfg.Links) {
		return
	}
	a.deleteIndex = row
	a.confirmDelete = true
}

func (a *App) doDelete() {
	row := a.deleteIndex
	if row < 0 || row >= len(a.cfg.Links) {
		return
	}
	c2 := cloneConfig(a.cfg)
	c2.Links = append(c2.Links[:row], c2.Links[row+1:]...)
	if err := c2.Save(a.cfgPath); err != nil {
		a.status = "删除条目失败：" + err.Error()
		return
	}
	name := a.cfg.Links[row].Name
	a.cfg = c2
	a.status = "已删除条目：" + name + "（链接和文件未改动）"
	a.resetRows()
	a.refresh()
}

// openFolder opens a folder in the shell; a test replaces it to see what a
// path click asks for without opening a window.
var openFolder = func(dir string) {
	if err := mygo.Shell.OpenPath(dir); err != nil {
		mygo.Shell.ShowItemInFolder(dir)
	}
}

// openPath opens the folder at path, or the closest folder above it that
// exists when path does not, level by level.
func (a *App) openPath(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	dir, ok := nearestDir(path)
	if !ok {
		a.status = "找不到可打开的目录：" + path
		return
	}
	openFolder(dir)
}

// nearestDir returns path when it is an existing directory, or the closest
// directory above it that exists. A path that is a file yields its folder.
func nearestDir(path string) (string, bool) {
	dir := filepath.Clean(path)
	for {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// ----- apply -----

func (a *App) beginApply() {
	if a.busy || a.scanning || a.readOnly {
		return
	}
	a.runApply(a.requests())
}

// applyOne applies the row the "创建" command belongs to, leaving every other
// row as it is.
func (a *App) applyOne(row int) {
	req, ok := a.createRequest(row)
	if !ok {
		return
	}
	a.runApply([]apply.Request{req})
}

// createRequest is the one-row "创建" command's request, and whether the row
// can be applied on its own now: it must be actionable by the scanner's rules,
// and a missing source must already carry its explicit intent. A conflict is
// not creatable until its target has been renamed away.
func (a *App) createRequest(row int) (apply.Request, bool) {
	if a.busy || a.scanning || a.readOnly {
		return apply.Request{}, false
	}
	res, ok := a.resultAt(row)
	if !ok {
		return apply.Request{}, false
	}
	switch res.Status {
	case scan.StatusMissing, scan.StatusReplaceable:
	case scan.StatusSourceMissing:
		if a.intents[row] != apply.IntentCreateSource {
			return apply.Request{}, false
		}
	default:
		return apply.Request{}, false
	}
	return apply.Request{Index: row, Checked: true, Intent: a.intents[row]}, true
}

func (a *App) runApply(reqs []apply.Request) {
	if len(reqs) == 0 {
		return
	}
	a.busy = true
	a.scanID++ // an in-flight scan is now stale
	a.scanning = false
	a.status = "正在应用…"
	cfg := cloneConfig(a.cfg)
	win := a.win
	scanner := a.scanner
	go func() {
		scanOne := func(i int) scan.Result {
			all := scanner.Scan(cfg)
			if i < 0 || i >= len(all) {
				return scan.Result{Index: i, Status: scan.StatusError, Message: "条目不存在"}
			}
			return all[i]
		}
		outs := apply.Run(cfg, reqs, scanOne, junctionLinker{}, apply.Options{})
		win.Update(func() {
			a.busy = false
			ok, failed, skipped, failedReqs := mergeOutcomes(a.outcomes, reqs, outs)
			a.failed = failedReqs
			a.status = fmt.Sprintf("应用完成：成功 %d 项，失败 %d 项，跳过 %d 项", ok, failed, skipped)
			a.refresh()
		})
	}()
}

func mergeOutcomes(dst map[int]apply.Outcome, reqs []apply.Request, outs []apply.Outcome) (ok, failed, skipped int, failedReqs []apply.Request) {
	byIndex := make(map[int]apply.Request, len(reqs))
	for _, r := range reqs {
		byIndex[r.Index] = r
	}
	for _, o := range outs {
		dst[o.Index] = o
		switch {
		case o.Failed():
			failed++
			if r, found := byIndex[o.Index]; found {
				failedReqs = append(failedReqs, r)
			}
		case o.Action == "跳过" || o.Action == "未选中":
			skipped++
		case o.Action == "无需处理":
		default:
			ok++
		}
	}
	return ok, failed, skipped, failedReqs
}

func (a *App) retryElevated() {
	if a.busy || len(a.failed) == 0 || a.cfgPath == "" {
		return
	}
	reqs := append([]apply.Request(nil), a.failed...)
	cfgPath := a.cfgPath
	a.busy = true
	a.status = "正在请求管理员权限…"
	win := a.win
	go func() {
		outs, err := runElevatedApply(cfgPath, reqs)
		win.Update(func() {
			a.busy = false
			if err != nil {
				if errors.Is(err, elevate.ErrCancelled) {
					a.status = "已取消管理员授权"
				} else {
					a.status = "管理员重试失败：" + err.Error()
				}
				return
			}
			ok, failed, skipped, failedReqs := mergeOutcomes(a.outcomes, reqs, outs)
			a.failed = failedReqs
			a.status = fmt.Sprintf("管理员重试完成：成功 %d 项，失败 %d 项，跳过 %d 项", ok, failed, skipped)
			a.refresh()
		})
	}()
}

func runElevatedApply(cfgPath string, reqs []apply.Request) ([]apply.Outcome, error) {
	dir, err := os.MkdirTemp("", "symlinkermanager-job-")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(dir)

	jobPath := filepath.Join(dir, "job.json")
	resultPath := filepath.Join(dir, "result.json")
	jf := job.File{
		ConfigPath: cfgPath,
		ResultPath: resultPath,
		Requests:   make([]job.Request, 0, len(reqs)),
	}
	for _, r := range reqs {
		jf.Requests = append(jf.Requests, job.Request{Index: r.Index, Checked: r.Checked, Intent: int(r.Intent)})
	}
	if err := job.WriteFile(jobPath, jf); err != nil {
		return nil, err
	}

	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("获取程序路径失败: %w", err)
	}
	if _, err := elevate.RunElevated(exe, []string{workerFlag, jobPath}); err != nil {
		return nil, err
	}

	res, err := job.ReadResult(resultPath)
	if err != nil {
		return nil, err
	}
	if res.Error != "" {
		return nil, errors.New(res.Error)
	}
	return res.ToApply(), nil
}

// ----- confirmations -----

func (a *App) alerts(c *ui.Context) {
	if a.confirmRemoveLink {
		res, _ := a.resultAt(a.removeLinkIndex)
		name := ""
		if a.removeLinkIndex >= 0 && a.removeLinkIndex < len(a.cfg.Links) {
			name = a.cfg.Links[a.removeLinkIndex].Name
		}
		msg := "只删除目标路径上的链接本身，不会删除数据盘中的源目录或其中的文件。\n\n" +
			name + "\n" + res.Target
		if ui.AlertDialog(c, &a.confirmRemoveLink, "移除链接？", msg, "取消", "移除链接") == 1 {
			a.doRemoveLink()
		}
	}

	if a.confirmDelete {
		res, _ := a.resultAt(a.deleteIndex)
		name := ""
		if a.deleteIndex >= 0 && a.deleteIndex < len(a.cfg.Links) {
			name = a.cfg.Links[a.deleteIndex].Name
		}
		msg := "只从配置中删除该条目，不会删除任何文件或链接。\n\n" + name + "\n" + res.Target
		if ui.AlertDialog(c, &a.confirmDelete, "删除条目？", msg, "取消", "删除") == 1 {
			a.doDelete()
		}
	}

	if a.confirmRename {
		res, _ := a.resultAt(a.renameIndex)
		name := ""
		if a.renameIndex >= 0 && a.renameIndex < len(a.cfg.Links) {
			name = a.cfg.Links[a.renameIndex].Name
		}
		msg := "只把该目录重命名为带时间戳的名称，不创建链接，也不会删除其中的文件。\n\n" +
			name + "\n" + res.Target + "\n\n重命名后该条目变为「待创建」，可再执行创建。"
		if ui.AlertDialog(c, &a.confirmRename, "重命名原目标？", msg, "取消", "重命名") == 1 {
			a.doRename()
		}
	}
}
