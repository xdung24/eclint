package eclint

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/editorconfig/editorconfig-core-go/v2"
	"github.com/go-logr/logr"
)

// FixWithDefinition does the hard work of validating the given file.
func FixWithDefinition(d *editorconfig.Definition, filename string, log logr.Logger) error {
	def, err := newDefinition(d)
	if err != nil {
		return err
	}

	stat, err := os.Stat(filename)
	if err != nil {
		return fmt.Errorf("cannot stat %s. %w", filename, err)
	}

	if stat.IsDir() {
		log.V(2).Info("skipped directory")
		return nil
	}

	fileSize := stat.Size()
	mode := stat.Mode()

	r, err := fixWithFilename(def, filename, fileSize, log)
	if err != nil {
		return err
	}

	if r == nil {
		return nil
	}

	// XXX keep mode as is.
	fp, err := os.OpenFile(filename, os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer fp.Close()

	n, err := io.Copy(fp, r)
	log.V(1).Info("bytes written", "total", n)

	return err
}

func fixWithFilename(def *definition, filename string, fileSize int64, log logr.Logger) (io.Reader, error) {
	fp, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s. %w", filename, err)
	}

	defer fp.Close()

	r := bufio.NewReader(fp)

	ok, err := probeReadable(fp, r)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s. %w", filename, err)
	}

	if !ok {
		log.V(2).Info("skipped unreadable or empty file")
		return nil, nil
	}

	charset, isBinary, err := ProbeCharsetOrBinary(r, def.Charset, log)
	if err != nil {
		return nil, err
	}

	if isBinary {
		log.V(2).Info("binary file detected and skipped")
		return nil, nil
	}

	log.V(2).Info("charset probed", "charset", charset)

	return fix(r, fileSize, charset, log, def)
}

func fix( // nolint: funlen
	r io.Reader,
	fileSize int64,
	charset string,
	log logr.Logger,
	def *definition,
) (io.Reader, error) {
	buf := bytes.NewBuffer([]byte{})

	var c []byte

	var x []byte

	size := def.IndentSize
	if def.TabWidth != 0 {
		size = def.TabWidth
	}

	switch def.IndentStyle {
	case SpaceValue:
		c = bytes.Repeat([]byte{space}, size)
		x = []byte{tab}
	case TabValue:
		c = []byte{tab}
		x = bytes.Repeat([]byte{space}, size)
	case "", UnsetValue:
		size = 0
	default:
		return nil, fmt.Errorf("%q is an invalid value of indent_style, want tab or space", def.IndentStyle)
	}

	var eol []byte

	switch def.EndOfLine {
	case "cr":
		eol = []byte{'\r'}
	case "crlf":
		eol = []byte{'\r', '\n'}
	case "lf":
		eol = []byte{'\n'}
	default:
		return nil, fmt.Errorf("unsupported EndOfLine value %s", def.EndOfLine)
	}

	trimTrailingWhitespace := false
	if def.TrimTrailingWhitespace != nil {
		trimTrailingWhitespace = *def.TrimTrailingWhitespace
	}

	errs := ReadLines(r, fileSize, func(index int, data []byte, isEOF bool) error {
		if size != 0 {
			data = fixTabAndSpacePrefix(data, c, x)
		}

		if trimTrailingWhitespace {
			data = fixTrailingWhitespace(data)
		}

		if def.EndOfLine != "" && !isEOF {
			data = bytes.TrimRight(data, "\r\n")

			data = append(data, eol...)
		}

		_, err := buf.Write(data)
		return err
	})

	if len(errs) != 0 {
		return nil, errs[0]
	}

	return buf, nil
}

// fixTabAndSpacePrefix replaces any `x` by `c` in the given `data`
func fixTabAndSpacePrefix(data []byte, c []byte, x []byte) []byte {
	newData := make([]byte, 0, len(data))

	i := 0
	for i < len(data) {
		if bytes.HasPrefix(data[i:], c) {
			i += len(c)

			newData = append(newData, c...)

			continue
		}

		if bytes.HasPrefix(data[i:], x) {
			i += len(x)

			newData = append(newData, c...)

			continue
		}

		return append(newData, data[i:]...)
	}

	return data
}

// fixTrailingWhitespace replaces any whitespace or tab from the end of the line
func fixTrailingWhitespace(data []byte) []byte {
	i := len(data) - 1

	// u -> v is the range to clean
	u := len(data)

	v := u

outer:
	for i >= 0 {
		switch data[i] {
		case '\r', '\n':
			i--
			u--
			v--
		case ' ', '\t':
			i--
			u--
		default:
			break outer
		}
	}

	if u != v {
		data = append(data[:u], data[v:]...)
	}

	return data
}
