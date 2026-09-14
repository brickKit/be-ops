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
		// Port 是这个组件单跑时监听的 HTTP 端口——合并部署下 local:true
		// 的 localPort 直接复用这个值，不是另外分配一个（导读："外壳
		// 启动器要从每个模块自己的 component.yaml 读端口，不许在外壳里
		// 另写一份端口表"，设计书 §13.8.1）。
		Port       int `yaml:"port"`
		ExtraPorts []struct {
			Name string `yaml:"name"`
			Port int    `yaml:"port"`
		} `yaml:"extraPorts"`
	} `yaml:"deployment"`
	ConfigSchema struct {
		Properties map[string]struct {
			// Default 用指针区分"没写 default 这个键"（nil，比如
			// iamJwksUrl/authzBundleUrl——没有默认值，只能靠
			// brickkit.yaml 的 config: 覆盖，缺了就该是"没这个值"）
			// 与"写了 default: \"\""（非 nil 指向空字符串，比如
			// otelBaseUrl——空字符串本身就是有意义的默认值，"→
			// Blackhole Exporter"）。
			Default *string `yaml:"default"`
		} `yaml:"properties"`
	} `yaml:"configSchema"`
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
		Port:         comp.Deployment.Port,
		Schema:       asm.Data.Schema,
		Role:         asm.Data.Role,
	}
	// ⚠️ local:true 的 localPort 直接复用组件自己单跑时的端口，不是另外
	// 分配一个——设计书 §13.8.1："外壳启动器要从每个模块自己的
	// component.yaml 读端口，不许在外壳里另写一份端口表"。这条只在
	// Shell 非空（这个组件确实要合并部署）时才生效，见 gen.go 的
	// ComponentSpec.Local 注释。
	if asm.Shell != "" {
		spec.Local = true
		spec.LocalPort = comp.Deployment.Port
	}
	if len(comp.Deployment.ExtraPorts) > 0 {
		spec.ExtraPorts = make(map[string]int, len(comp.Deployment.ExtraPorts))
		for _, p := range comp.Deployment.ExtraPorts {
			spec.ExtraPorts[p.Name] = p.Port
		}
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
	for key, prop := range comp.ConfigSchema.Properties {
		if prop.Default == nil {
			continue
		}
		if spec.ConfigDefaults == nil {
			spec.ConfigDefaults = map[string]string{}
		}
		spec.ConfigDefaults[key] = *prop.Default
	}
	return spec, true, nil
}

// brickkitYAML 只解析 brickkit.yaml 里 shell 装配需要的那一小块——
// 每个组件条目的 id + config:（阶段四附加 Task 0.2）。不是
// brickkit.yaml 的完整镜像，其余字段（local/expose/labels……）不关心。
type brickkitYAML struct {
	Components []struct {
		ID     string            `yaml:"id"`
		Config map[string]string `yaml:"config"`
	} `yaml:"components"`
}

// LoadBrickkitConfig 读装配仓库根目录的 brickkit.yaml，返回
// componentID → config 字面量覆盖的映射。这些值（比如
// authzBundleUrl/iamJwksUrl）是手写在 brickkit.yaml 里的，不在任何
// component.yaml/assembly.yaml 里——genyaml.Load 读不到，必须单独
// 读这一份文件（阶段四附加 Task 0.2 调研记录：这条数据 servedBy 场景下
// 没有平台产物可以借用，只能自己从两份已知 YAML 合并出来）。
func LoadBrickkitConfig(path string) (map[string]map[string]string, error) {
	var doc brickkitYAML
	if err := readYAML(path, &doc); err != nil {
		return nil, err
	}
	out := make(map[string]map[string]string, len(doc.Components))
	for _, c := range doc.Components {
		if len(c.Config) == 0 {
			continue
		}
		out[c.ID] = c.Config
	}
	return out, nil
}

// MergeConfig 合并"component.yaml 的默认值"与"brickkit.yaml 的字面量
// 覆盖"：两边都没有的 key 不出现在结果里；只有默认值的 key 用默认值；
// brickkit.yaml 写了的 key 覆盖默认值（不管默认值是否存在）。这条合并
// 规则跟 brickKit 自己的注入引擎对 configSchema 项的既有语义一致——
// 都是读同一份 brickkit.yaml/component.yaml，不是凭空另算一套。
func MergeConfig(defaults, overrides map[string]string) map[string]string {
	if len(defaults) == 0 && len(overrides) == 0 {
		return nil
	}
	out := make(map[string]string, len(defaults)+len(overrides))
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

func readYAML(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, out)
}
