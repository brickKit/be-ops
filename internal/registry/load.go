// Package registry 读写 registry/ 下的全局册子（ports.tsv / schemas.tsv /
// permissions.tsv / data-scopes.tsv）。be-ops 的多个子命令（registry / gen /
// permissions / data-scopes）共用同一套解析逻辑，不许各自再实现一遍。
//
// 判据从 infra/scripts/registry-check.sh 原样搬过来（阶段一 Task 4 已经
// 实测修过两处），本脚本因此退休为薄壳。
package registry

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// PortRow 对应 ports.tsv 的一行（6 列）。
type PortRow struct {
	Repo        string
	ComponentID string
	HTTPPort    string
	GRPCPort    string
	Shell       string
	Note        string
}

// SchemaRow 对应 schemas.tsv 的一行（4 列）。
type SchemaRow struct {
	Repo           string
	Schema         string
	Role           string
	ShellLoginRole string
}

func LoadPorts(path string) ([]PortRow, error) {
	lines, err := readDataLines(path)
	if err != nil {
		return nil, err
	}
	rows := make([]PortRow, 0, len(lines))
	for _, l := range lines {
		cols := strings.Split(l.text, "\t")
		if len(cols) != 6 {
			return nil, fmt.Errorf("%s 第 %d 行不是 6 列：%s", path, l.num, l.text)
		}
		rows = append(rows, PortRow{
			Repo: cols[0], ComponentID: cols[1], HTTPPort: cols[2],
			GRPCPort: cols[3], Shell: cols[4], Note: cols[5],
		})
	}
	return rows, nil
}

func LoadSchemas(path string) ([]SchemaRow, error) {
	lines, err := readDataLines(path)
	if err != nil {
		return nil, err
	}
	rows := make([]SchemaRow, 0, len(lines))
	for _, l := range lines {
		cols := strings.Split(l.text, "\t")
		if len(cols) != 4 {
			return nil, fmt.Errorf("%s 第 %d 行不是 4 列：%s", path, l.num, l.text)
		}
		rows = append(rows, SchemaRow{
			Repo: cols[0], Schema: cols[1], Role: cols[2], ShellLoginRole: cols[3],
		})
	}
	return rows, nil
}

type dataLine struct {
	num  int
	text string
}

// readDataLines 跳过空行与 # 开头的注释行——两张表都用这个约定。
func readDataLines(path string) ([]dataLine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []dataLine
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, dataLine{num: n, text: line})
	}
	return out, sc.Err()
}
