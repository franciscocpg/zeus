/*
 *  ZEUS - A Powerful Build System
 *  Copyright (c) 2017 Philipp Mieden <dreadl0ck@protonmail.ch>
 *
 *  This program is free software: you can redistribute it and/or modify
 *  it under the terms of the GNU General Public License as published by
 *  the Free Software Foundation, either version 3 of the License, or
 *  (at your option) any later version.
 *
 *  This program is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 *  GNU General Public License for more details.
 *
 *  You should have received a copy of the GNU General Public License
 *  along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mvdan/sh/syntax"
)

var (
	// ErrNotAShellScript means its not a shellscript
	ErrNotAShellScript = errors.New("file is not a shellscript")

	// ErrNoDirectory means its not a directory
	ErrNoDirectory = errors.New("not a directory")
)

type shellConfidence int

const (
	notShellFile shellConfidence = iota
	ifValidShebang
	isShellFile
)

// generic formatter type
// contains all relevant information for formatting scripts
type formatter struct {

	// buffers
	readBuf  bytes.Buffer
	writeBuf bytes.Buffer

	language      string
	fileExtension string

	openMode    int
	parseMode   syntax.ParseMode
	printConfig syntax.PrintConfig

	// regexes
	validShebang *regexp.Regexp
	shellFile    *regexp.Regexp
}

// initialize the formatter to handle shell scripts
func newFormatter() *formatter {
	return &formatter{
		readBuf:  bytes.Buffer{},
		writeBuf: bytes.Buffer{},

		language:      "bash",
		fileExtension: ".sh",

		openMode:  os.O_RDWR,
		parseMode: syntax.ParseComments,

		validShebang: regexp.MustCompile(`^#!\s?/(usr/)?bin/(env *)?(sh|bash)`),
		shellFile:    regexp.MustCompile(`\.(sh|bash)$`),
	}
}

// check if its a valid script
func isValidScript(info os.FileInfo) shellConfidence {

	name := info.Name()

	switch {
	case info.IsDir(), name[0] == '.', !info.Mode().IsRegular():
		return notShellFile
	case f.shellFile.MatchString(name):
		return isShellFile
	case strings.Contains(name, "."):
		return notShellFile // different extension
	case info.Size() < 8:
		return notShellFile // cannot possibly hold valid shebang
	default:
		return ifValidShebang
	}
}

// format a single shell file on disk
func (f *formatter) formatPath(path string) error {

	var cLog = Log.WithField("prefix", "formatPath")
	cLog.Debug(path)

	// open file at path
	file, err := os.OpenFile(path, f.openMode, 0)
	if err != nil {
		return err
	}
	defer file.Close()

	// flush buffer
	f.readBuf.Reset()

	// copy file content into buffer
	if _, err := io.Copy(&f.readBuf, file); err != nil {
		return err
	}

	// check bang
	src := f.readBuf.Bytes()
	if !f.validShebang.Match(src[:32]) {
		return nil
	}

	// parse
	prog, err := syntax.Parse(src, path, f.parseMode)
	if err != nil {
		return err
	}

	// flush buffer
	f.writeBuf.Reset()

	// format
	f.printConfig.Fprint(&f.writeBuf, prog)
	res := f.writeBuf.Bytes()

	// check if there were changes and input
	if !bytes.Equal(src, res) && len(res) > 0 {

		// truncate file
		if err := empty(file); err != nil {
			return err
		}

		// write result
		if _, err := file.Write(res); err != nil {
			return err
		}
	}
	return nil
}

// walk the zeus directoy and run formatPath on all files
func (f *formatter) formatzeusDir() error {

	var cLog = Log.WithField("prefix", "formatzeusDir")

	info, err := os.Stat(zeusDir)
	if err != nil {
		cLog.WithError(err).Error("path does not exist")
		return err
	}
	if !info.IsDir() {
		return ErrNoDirectory
	}

	return filepath.Walk(zeusDir, func(path string, info os.FileInfo, err error) error {

		// no recursion for now
		if info.IsDir() {
			return nil
		}

		if err != nil {
			cLog.WithError(err).Error("error walking zeus directory")
			return err
		}

		conf := isValidScript(info)
		if conf == notShellFile {
			return ErrNotAShellScript
		}

		err = f.formatPath(path)
		if err != nil && !os.IsNotExist(err) {
			cLog.WithError(err).Error("failed to format path: " + path)
			return err
		}
		return nil
	})
}

/*
 *	Utils
 */

// truncate file and seek to the beginning
func empty(f *os.File) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err := f.Seek(0, 0)
	return err
}

// run the formatter for all files in the zeus dir
// calculates runtime and displays error
func (f *formatter) formatCommand() {

	var (
		start = time.Now()
		err   = f.formatzeusDir()
	)
	if err != nil {
		l.Println("error formatting: ", err)
	}
	l.Println(printPrompt()+"formatted zeus directory in ", time.Now().Sub(start))
}

// watch the zeus dir changes and run format on write event
func (f *formatter) watchzeusDir() {

	err := addEvent(zeusDir, fsnotify.Write, func(event fsnotify.Event) {

		// check if its a valid script
		if strings.HasSuffix(event.Name, f.fileExtension) {

			// format script
			err := f.formatPath(event.Name)
			if err != nil {
				Log.WithError(err).Error("failed to format file")
			}
		}

	}, "")
	if err != nil {
		Log.Error("failed to watch path: ", zeusDir)
	}
}
