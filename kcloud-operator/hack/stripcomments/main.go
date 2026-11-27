// ============================================================
// main.go: Go 파일에서 주석을 뺀 코드만 표준 출력에 쓴다
// 상세: 두 판본의 출력을 비교해 주석만 바뀐 커밋인지 판정하는 데 쓴다. 인자가 "-" 면 표준 입력을 읽는다.
// 주석만 있는 줄은 먼저 지운다(그 줄의 추가·삭제가 printer 의 빈 줄 배치를 바꾸므로).
// 생성일: 2026-10-11 | 수정일: 2026-10-13
// ============================================================
package main

import (
	"bytes"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"io"
	"log"
	"os"
	"strings"
)

// dropCommentLines 는 주석만 있는 줄을 지운다. 그 줄의 추가·삭제가 printer 출력의 빈 줄 배치를
// 바꿔 코드 변경으로 잘못 판정되는 것을 막는다. 코드 토큰이 하나라도 있는 줄(여러 줄 raw string
// 포함)은 남기고, 여러 줄 블록 주석은 걸친 줄 어디에든 코드가 있으면 통째로 남긴다. 줄 번호는
// //line 지시문을 무시한 물리 줄이다(지시문 뒤 줄 번호로 판정하면 코드 줄을 지울 수 있다).
func dropCommentLines(src []byte) []byte {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, scanner.ScanComments)
	type span struct{ from, to int }
	var comments []span
	code, comment := map[int]bool{}, map[int]bool{}
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON && lit == "\n" {
			continue // 자동 삽입 세미콜론은 코드가 아니다
		}
		from := fset.PositionFor(pos, false).Line // //line 지시문을 따르지 않는 물리 줄
		to := from + strings.Count(lit, "\n")
		if tok == token.COMMENT {
			comments = append(comments, span{from, to})
			for l := from; l <= to; l++ {
				comment[l] = true
			}
			continue
		}
		for l := from; l <= to; l++ {
			code[l] = true
		}
	}
	// 한 줄이라도 코드와 겹치는 블록 주석은 전 구간을 남긴다(일부만 지우면 주석이 깨진다).
	for changed := true; changed; {
		changed = false
		for _, c := range comments {
			shared := false
			for l := c.from; l <= c.to; l++ {
				shared = shared || code[l]
			}
			for l := c.from; shared && l <= c.to; l++ {
				if !code[l] {
					code[l], changed = true, true
				}
			}
		}
	}
	var out []byte
	for i, ln := range bytes.SplitAfter(src, []byte("\n")) {
		if n := i + 1; comment[n] && !code[n] {
			continue
		}
		out = append(out, ln...)
	}
	return out
}

func main() {
	log.SetFlags(0)
	if len(os.Args) != 2 {
		log.Fatal("용법: stripcomments <file.go|->")
	}
	var src []byte
	var err error
	if os.Args[1] == "-" {
		src, err = io.ReadAll(os.Stdin)
	} else {
		src, err = os.ReadFile(os.Args[1])
	}
	if err != nil {
		log.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "input.go", dropCommentLines(src), parser.SkipObjectResolution)
	if err != nil {
		log.Fatal(err)
	}
	if err := printer.Fprint(os.Stdout, fset, f); err != nil {
		log.Fatal(err)
	}
}
