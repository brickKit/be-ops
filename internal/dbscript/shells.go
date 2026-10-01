package dbscript

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadShells 读 <root>/shell/be/*/component.yaml 的 shell.members。
// shell/be/ 不存在或为空时返回空切片（外壳清单由别的任务创建，此前只建
// 登录角色）。成员写作 "<id>@<version>"，这里只保留 id。
func LoadShells(root string) ([]Shell, error) {
	paths, err := filepath.Glob(filepath.Join(root, "shell", "be", "*", "component.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var shells []Shell
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Shell struct {
				Members []string `yaml:"members"`
			} `yaml:"shell"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("%s：%w", p, err)
		}
		sh := Shell{Name: filepath.Base(filepath.Dir(p))}
		for _, m := range doc.Shell.Members {
			id, _, _ := strings.Cut(m, "@")
			sh.Members = append(sh.Members, strings.TrimSpace(id))
		}
		shells = append(shells, sh)
	}
	return shells, nil
}
