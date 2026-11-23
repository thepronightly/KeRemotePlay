//go:build !windows

package main

import "fmt"

func runWindow(url string) {
	fmt.Println("KeRemotePlay UI:", url)
	<-quitCh
}
