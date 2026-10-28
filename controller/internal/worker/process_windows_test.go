//go:build windows

package worker

import (
	"bufio"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestOwnedJobEndsOnlyItsChildren(t *testing.T) {
	if os.Getenv("PARTY_JOB_TEST") == "child" {
		bufio.NewReader(os.Stdin).ReadString('\n')
		c := exec.Command(os.Args[0], "-test.run=TestOwnedJobEndsOnlyItsChildren")
		c.Env = append(os.Environ(), "PARTY_JOB_TEST=grandchild")
		if e := c.Start(); e != nil {
			os.Exit(2)
		}
		fmt.Println(c.Process.Pid)
		for {
			time.Sleep(time.Second)
		}
	}
	if os.Getenv("PARTY_JOB_TEST") == "grandchild" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	j, e := newJob()
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	c := exec.Command(os.Args[0], "-test.run=TestOwnedJobEndsOnlyItsChildren")
	c.Env = append(os.Environ(), "PARTY_JOB_TEST=child")
	in, _ := c.StdinPipe()
	out, _ := c.StdoutPipe()
	if e = c.Start(); e != nil {
		t.Fatal(e)
	}
	defer c.Process.Kill()
	if e = j.Assign(c.Process); e != nil {
		t.Fatal(e)
	}
	fmt.Fprintln(in, "start")
	line, e := bufio.NewReader(out).ReadString('\n')
	if e != nil {
		t.Fatal(e)
	}
	pid, e := strconv.Atoi(stringTrim(line))
	if e != nil {
		t.Fatal(e)
	}
	h, e := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if e != nil {
		t.Fatal(e)
	}
	defer windows.CloseHandle(h)
	unrelated := exec.Command(os.Args[0], "-test.run=TestOwnedJobEndsOnlyItsChildren")
	unrelated.Env = append(os.Environ(), "PARTY_JOB_TEST=grandchild")
	if e = unrelated.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { unrelated.Process.Kill(); unrelated.Wait() }()
	if e = j.Close(); e != nil {
		t.Fatal(e)
	}
	c.Wait()
	result, e := windows.WaitForSingleObject(h, 5000)
	if e != nil || result != windows.WAIT_OBJECT_0 {
		t.Fatalf("owned grandchild survived: %v %v", result, e)
	}
	u, e := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(unrelated.Process.Pid))
	if e != nil {
		t.Fatal("unrelated process ended")
	}
	defer windows.CloseHandle(u)
	result, e = windows.WaitForSingleObject(u, 0)
	if e != nil || result != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("unrelated process affected: %v %v", result, e)
	}
}
func stringTrim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
