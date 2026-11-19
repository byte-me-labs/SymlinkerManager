package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"symlinkermanager/internal/apply"
	"symlinkermanager/internal/config"
	"symlinkermanager/internal/scan"
)

// openRowMenu opens the action menu of the row showing rowLabel. The table
// header also shows "操作", so clicking the label by name would hit the
// header; click the row's button instead, at the header's column.
func openRowMenu(t *testing.T, tt *ui.Tester, rowLabel string) {
	t.Helper()
	head, ok := tt.Find("操作")
	if !ok {
		t.Fatal("找不到操作列")
	}
	row, ok := tt.Find(rowLabel)
	if !ok {
		t.Fatalf("找不到行 %q", rowLabel)
	}
	tt.ClickAt(head.X+head.W/2, row.Y+row.H/2)
	if len(tt.Menu()) == 0 {
		t.Fatalf("行 %q 的操作菜单未打开", rowLabel)
	}
}

// newTestApp builds an App with the given rows already scanned, ready for
// ui.NewTester to draw without touching the disk.
func newTestApp(links []config.Link, results []scan.Result) *App {
	a := newApp()
	a.cfgPath = `C:\portable\config.json`
	a.cfg = config.New()
	a.cfg.Links = links
	a.checked = make([]bool, len(links))
	for i := range a.checked {
		a.checked[i] = true
	}
	a.results = results
	return a
}

func TestActionMenuListsRowCommands(t *testing.T) {
	a := newTestApp(
		[]config.Link{
			{Name: "冲突目录", Source: `D:\data\a`, Target: `C:\links\a`},
			{Name: "正确目录", Source: `D:\data\b`, Target: `C:\links\b`},
		},
		[]scan.Result{
			{Index: 0, Status: scan.StatusConflict, Info: scan.TargetInfo{Kind: scan.TargetDir}},
			{Index: 1, Status: scan.StatusOK, Info: scan.TargetInfo{Kind: scan.TargetJunction, LinkTarget: `D:\data\b`}},
		},
	)
	tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1420, 860)

	if !tt.HasText("更多") {
		t.Fatalf("行的命令菜单按钮应显示为更多: texts=%q", tt.Texts())
	}
	openRowMenu(t, tt, "冲突目录")
	got := tt.Menu()
	want := []string{"创建", "编辑", "移除链接", "-", "删除"}
	if len(got) != len(want) {
		t.Fatalf("菜单项 = %q, 期望 %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("菜单项 = %q, 期望 %q", got, want)
		}
	}
	tt.CloseMenu()

	// The fix for a conflict is a button at the end of its target path. It
	// only renames the existing directory, so it asks for confirmation and
	// never feeds the batch-create intents.
	if err := tt.Click("重命名"); err != nil {
		t.Fatalf("点击重命名: %v", err)
	}
	if !a.confirmRename || a.renameIndex != 0 {
		t.Fatalf("重命名确认未打开: confirm=%v index=%d", a.confirmRename, a.renameIndex)
	}
	if a.intents[0] != apply.IntentAuto {
		t.Fatalf("重命名不应改变批量创建意图: intent=%v", a.intents[0])
	}
}

func TestSelectAllLeavesConflictsUnchecked(t *testing.T) {
	a := newTestApp(
		[]config.Link{
			{Name: "待创建", Source: `D:\data\a`, Target: `C:\links\a`},
			{Name: "冲突", Source: `D:\data\b`, Target: `C:\links\b`},
		},
		[]scan.Result{
			{Index: 0, Status: scan.StatusMissing},
			{Index: 1, Status: scan.StatusConflict},
		},
	)
	for i := range a.checked {
		a.checked[i] = false
	}
	a.selectAll(true)
	if !a.checked[0] || a.checked[1] {
		t.Fatalf("全选只应勾选可批量处理的条目: %v", a.checked)
	}
}

func TestCreateRequestNeedsActionableRow(t *testing.T) {
	a := newTestApp(
		[]config.Link{
			{Name: "待创建", Source: `D:\data\a`, Target: `C:\links\a`},
			{Name: "已正确", Source: `D:\data\b`, Target: `C:\links\b`},
			{Name: "冲突", Source: `D:\data\c`, Target: `C:\links\c`},
			{Name: "源缺失", Source: `D:\data\d`, Target: `C:\links\d`},
		},
		[]scan.Result{
			{Index: 0, Status: scan.StatusMissing, Info: scan.TargetInfo{Kind: scan.TargetMissing}},
			{Index: 1, Status: scan.StatusOK, Info: scan.TargetInfo{Kind: scan.TargetJunction}},
			{Index: 2, Status: scan.StatusConflict, Info: scan.TargetInfo{Kind: scan.TargetDir}},
			{Index: 3, Status: scan.StatusSourceMissing, Info: scan.TargetInfo{Kind: scan.TargetMissing}},
		},
	)

	if req, ok := a.createRequest(0); !ok || req.Index != 0 || !req.Checked {
		t.Fatalf("待创建行应可单独创建: req=%+v ok=%v", req, ok)
	}
	if _, ok := a.createRequest(1); ok {
		t.Fatal("已正确行不应可创建")
	}
	if _, ok := a.createRequest(2); ok {
		t.Fatal("冲突行在重命名前不应可创建")
	}
	if _, ok := a.createRequest(3); ok {
		t.Fatal("未选择创建源目录的源缺失行不应可创建")
	}
	a.intents[3] = apply.IntentCreateSource
	if _, ok := a.createRequest(3); !ok {
		t.Fatal("已选择创建源目录的源缺失行应可创建")
	}
	a.readOnly = true
	if _, ok := a.createRequest(0); ok {
		t.Fatal("只读模式下不应可创建")
	}
}

func TestCreateSourceButtonOnSourcePath(t *testing.T) {
	a := newTestApp(
		[]config.Link{{Name: "源缺失", Source: `D:\data\missing`, Target: `C:\links\missing`}},
		[]scan.Result{{Index: 0, Status: scan.StatusSourceMissing, Info: scan.TargetInfo{Kind: scan.TargetMissing}}},
	)
	tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1420, 860)

	if err := tt.Click("创建源目录"); err != nil {
		t.Fatalf("点击创建源目录: %v", err)
	}
	if a.intents[0] != apply.IntentCreateSource || !a.checked[0] {
		t.Fatalf("创建源目录未生效: intent=%v checked=%v", a.intents[0], a.checked[0])
	}
	if _, ok := a.createRequest(0); !ok {
		t.Fatal("开启创建源目录后应可单独创建")
	}
}

func TestActionMenuEditOpensDialog(t *testing.T) {
	a := newTestApp(
		[]config.Link{{Name: "文档目录", Source: `D:\data\docs`, Target: `C:\links\docs`}},
		[]scan.Result{{Index: 0, Status: scan.StatusOK, Info: scan.TargetInfo{Kind: scan.TargetJunction}}},
	)
	tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1420, 860)

	openRowMenu(t, tt, "文档目录")
	if err := tt.ChooseMenuItem("编辑"); err != nil {
		t.Fatalf("选择编辑: %v", err)
	}
	if !a.modalOpen || a.editingIndex != 0 {
		t.Fatalf("编辑弹窗未打开: modalOpen=%v editingIndex=%d", a.modalOpen, a.editingIndex)
	}
	if !tt.HasText("编辑条目") {
		t.Fatalf("弹窗标题未显示, texts=%q", tt.Texts())
	}
}

func TestActionMenuDisablesInReadOnly(t *testing.T) {
	a := newTestApp(
		[]config.Link{{Name: "文档目录", Source: `D:\data\docs`, Target: `C:\links\docs`}},
		[]scan.Result{{Index: 0, Status: scan.StatusOK, Info: scan.TargetInfo{Kind: scan.TargetJunction}}},
	)
	a.readOnly = true
	tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1420, 860)

	openRowMenu(t, tt, "文档目录")
	// 只读时编辑无法打开，选择会报错。
	if err := tt.ChooseMenuItem("编辑"); err == nil {
		t.Fatal("只读模式下编辑菜单项应禁用")
	}
	tt.CloseMenu()
}
