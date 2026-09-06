package genyaml

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// componentYAML 只解析 genyaml 关心的那几个字段——不是 component.yaml
// 的完整镜像。metadata.id 的正则、健康检查等交给别的产出去校验。
type componentYAML struct {
	Metadata struct {
		ID      string `yaml:"id"`
		Version string `yaml:"version"`
	} `yaml:"metadata"`
	Dependencies struct {
		Components []dependencyEntry `yaml:"components"`
		Resources  []struct {
			Kind   string `yaml:"kind"`
			Engine string `yaml:"engine"`
		} `yaml:"resources"`
	} `yaml:"dependencies"`
	Deployment struct {
		Labels map[string]string `yaml:"labels"`
	} `yaml:"deployment"`
}

// dependencyEntry 兼容两种写法：纯字符串 "mdm/customer@1.0.0"，或
// { id: infra/workflow@1.0.0, optional: true }（§3.6）。
type dependencyEntry struct {
	raw      string
	ID       string `yaml:"id"`
	Optional bool   `yaml:"optional"`
}

func (d *dependencyEntry) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		d.raw = node.Value
		return nil
	}
	type plain dependencyEntry
	return node.Decode((*plain)(d))
}

type assemblyYAML struct {
	ID    string `yaml:"id"`
	Asset struct {
		AssemblyRole string `yaml:"assembly_role"`
		SlotName     string `yaml:"slot_name"`
	} `yaml:"asset"`
	Shell string `yaml:"shell"`
	Data  struct {
		Schema string `yaml:"schema"`
		Role   string `yaml:"role"`
	} `yaml:"data"`
}

// stripVersionSuffix 把 "mdm/customer@1.0.0" 切成 "mdm/customer"——
// dependencyEntry 的裸字符串写法自带版本号，genyaml 目前只关心 ID。
func stripVersionSuffix(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '@' {
			return s[:i]
		}
	}
	return s
}

// Load 扫描 root 下所有 */*/component.yaml + assembly.yaml，合并成
// ComponentSpec。目录结构是 <domain>/<name>/（组件 ID 的两段），匹配
// components/ 的实际布局。
func Load(root string) ([]ComponentSpec, error) {
	var specs []ComponentSpec

	domains, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, domain := range domains {
		if !domain.IsDir() {
			continue
		}
		domainPath := filepath.Join(root, domain.Name())
		names, err := os.ReadDir(domainPath)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if !name.IsDir() {
				continue
			}
			dir := filepath.Join(domainPath, name.Name())
			spec, ok, err := loadOne(dir)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", dir, err)
			}
			if ok {
				specs = append(specs, spec)
			}
		}
	}
	return specs, nil
}

func loadOne(dir string) (ComponentSpec, bool, error) {
	compPath := filepath.Join(dir, "component.yaml")
	asmPath := filepath.Join(dir, "assembly.yaml")
	if _, err := os.Stat(compPath); os.IsNotExist(err) {
		return ComponentSpec{}, false, nil
	}

	var comp componentYAML
	if err := readYAML(compPath, &comp); err != nil {
		return ComponentSpec{}, false, err
	}
	var asm assemblyYAML
	if err := readYAML(asmPath, &asm); err != nil {
		return ComponentSpec{}, false, err
	}

	spec := ComponentSpec{
		ID:           comp.Metadata.ID,
		Version:      comp.Metadata.Version,
		AssemblyRole: asm.Asset.AssemblyRole,
		SlotName:     asm.Asset.SlotName,
		Shell:        asm.Shell,
	}
	if len(comp.Deployment.Labels) > 0 {
		spec.Labels = make(map[string]any, len(comp.Deployment.Labels))
		for k, v := range comp.Deployment.Labels {
			spec.Labels[k] = v
		}
	}
	for _, d := range comp.Dependencies.Components {
		if d.raw != "" {
			spec.Dependencies = append(spec.Dependencies, Dependency{ID: stripVersionSuffix(d.raw)})
		} else {
			spec.Dependencies = append(spec.Dependencies, Dependency{ID: stripVersionSuffix(d.ID), Optional: d.Optional})
		}
	}
	for _, r := range comp.Dependencies.Resources {
		spec.Resources = append(spec.Resources, ResourceDep{Kind: r.Kind, Engine: r.Engine})
	}
	return spec, true, nil
}

func readYAML(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, out)
}
