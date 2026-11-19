// SymlinkerManager is a portable Windows directory-junction manager. Its
// window is drawn by MyGo's native UI; there is no web page and no webview.
package main

import (
	"log"
	"os"

	"github.com/egoist/mygo"

	"symlinkermanager/internal/job"
)

// workerFlag turns this executable into the headless, elevated helper. It is
// checked before MyGo starts so the helper never creates a window and never
// takes the single instance lock.
const workerFlag = "--elevated-job"

func main() {
	if len(os.Args) >= 2 && os.Args[1] == workerFlag {
		os.Exit(runWorker(os.Args[2:]))
	}

	mygo.App.SetName("SymlinkerManager")
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	a := newApp()
	mygo.App.OnSecondInstance(func(args []string, workingDir string) {
		a.activate()
	})
	mygo.App.WhenReady(a.start)
	if err := mygo.App.Run(); err != nil {
		log.Printf("运行失败: %v", err)
	}
}

func runWorker(args []string) int {
	if len(args) < 1 {
		return 2
	}
	f, err := job.ReadFile(args[0])
	if err != nil {
		return 2
	}
	res := job.Run(f)
	if err := job.WriteResult(f.ResultPath, res); err != nil {
		return 3
	}
	return 0
}
