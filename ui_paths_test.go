package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"symlinkermanager/internal/config"
	"symlinkermanager/internal/scan"
)

// TestPathsShowWholeValues keeps the source and target cells showing the
// whole path: a narrow column shortens the drawing, not the value, and the
// tooltip has the same whole path.
func TestPathsShowWholeValues(t *testing.T) {
	source := `D:\VeryLongDataFolder\SteamLibrary\steamapps\common\SomeGame\userdata\12345678`
	target := `C:\Users\Luomo\AppData\Roaming\SomeVendor\SomeGame\Profiles\12345678`
	a := newTestApp(
		[]config.Link{{Name: "长路径", Source: source, Target: target}},
		[]scan.Result{{Index: 0, Status: scan.StatusOK, Info: scan.TargetInfo{Kind: scan.TargetJunction}}},
	)
	tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1180, 640)

	if !tt.HasText(source) {
		t.Fatalf("源路径未按原样显示: texts=%q", tt.Texts())
	}
	if !tt.HasText(target) {
		t.Fatalf("目标路径未按原样显示: texts=%q", tt.Texts())
	}
}

// TestNameColumnFitsItsContent gives the name column a width that fits its
// longest name, between its minimum and maximum, instead of an equal share of
// the window: short names leave the room to the paths.
func TestNameColumnFitsItsContent(t *testing.T) {
	cases := []struct {
		name  string
		want  float32 // the name column's width
		label string
	}{
		{"短名称", 100, "短名称"},
		{"这是一个非常非常长的目录名称用来检查名称列不会无限变宽而是停在最大值", 240, "长名称截到上限"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			a := newTestApp(
				[]config.Link{{Name: tc.name, Source: `D:\data\docs`, Target: `C:\links\docs`}},
				[]scan.Result{{Index: 0, Status: scan.StatusOK, Info: scan.TargetInfo{Kind: scan.TargetJunction}}},
			)
			tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, 1420, 860)
			name, ok := tt.Find("名称")
			if !ok {
				t.Fatal("找不到名称列")
			}
			source, ok := tt.Find("源目录")
			if !ok {
				t.Fatal("找不到源目录列")
			}
			// Both headers start one cell padding inside their column, so the
			// distance between them is the name column's width.
			got := source.X - name.X
			if got != tc.want {
				t.Fatalf("名称列宽 = %v, 期望 %v", got, tc.want)
			}
		})
	}
}

// TestColumnsStayInsideWindow drags the edge of the target column: the status
// and action columns must keep their place, so they never leave the window.
func TestColumnsStayInsideWindow(t *testing.T) {
	source := `D:\VeryLongDataFolder\SteamLibrary\steamapps\common\SomeGame\userdata\12345678`
	target := `C:\Users\Luomo\AppData\Roaming\SomeVendor\SomeGame\Profiles\12345678`
	a := newTestApp(
		[]config.Link{{Name: "长路径", Source: source, Target: target}},
		[]scan.Result{{Index: 0, Status: scan.StatusOK, Info: scan.TargetInfo{Kind: scan.TargetJunction}}},
	)
	const width, height = 1180, 640
	tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, width, height)

	actionBefore, ok := tt.Find("操作")
	if !ok {
		t.Fatal("找不到操作列")
	}
	head, ok := tt.Find("目标路径")
	if !ok {
		t.Fatal("找不到目标路径列")
	}
	status, ok := tt.Find("状态")
	if !ok {
		t.Fatal("找不到状态列")
	}

	// The resize grip sits at the target column's right edge, just left of
	// the status column. The status text starts one cell padding (10px)
	// inside its column, and the grip is the 8px before that edge.
	x, y := status.X-14, head.Y+head.H/2
	tt.Press(x, y)
	tt.Move(x+120, y)
	tt.Move(x+120, y)
	tt.Release(x+120, y)

	action, ok := tt.Find("操作")
	if !ok {
		t.Fatal("调整后找不到操作列")
	}
	if action.X != actionBefore.X || action.X+action.W > width {
		t.Fatalf("操作列被推出窗口: before=%v after=%v width=%v", actionBefore, action, width)
	}
	statusAfter, ok := tt.Find("状态")
	if !ok || statusAfter.X != status.X {
		t.Fatalf("状态列被推走: before=%v after=%v", status, statusAfter)
	}
}
