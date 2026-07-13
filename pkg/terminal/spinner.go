package terminal

import (
	"fmt"
	"sync"
	"time"
)

// StartSpinner 在终端显示一个带文字的加载动画，返回一个用于停止动画的函数
// 参数 clearLine 决定停止时是否清除该行内容
func StartSpinner(msg string) func(bool) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		frames := []string{"[=    ]", "[ =   ]", "[  =  ]", "[   = ]", "[    =]", "[   = ]", "[  =  ]", "[ =   ]"}
		i := 0
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		fmt.Printf("\r%s %s", msg, frames[0])
		i++
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Printf("\r%s %s", msg, frames[i%len(frames)])
				i++
			}
		}
	}()

	var once sync.Once
	return func(clearLine bool) {
		once.Do(func() {
			close(done)
			wg.Wait()
			if clearLine {
				fmt.Printf("\r\033[K")
			}
		})
	}
}
