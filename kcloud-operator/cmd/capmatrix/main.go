// ============================================================
// main.go: 호환성 매트릭스 문서 생성기 (go run ./cmd/capmatrix -out <path>)
// 생성일: 2026-09-19
// ============================================================

package main

import (
	"flag"
	"fmt"
	"os"

	"kcloud-operator/internal/vendorcap"
)

func main() {
	out := flag.String("out", "", "출력 경로(비우면 stdout)")
	flag.Parse()
	md := vendorcap.RenderMatrix()
	if *out == "" {
		fmt.Print(md)
		return
	}
	if err := os.WriteFile(*out, []byte(md), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
