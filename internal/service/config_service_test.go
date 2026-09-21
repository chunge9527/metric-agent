package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	myconstant "metric-agent/internal/common/constant"
	"metric-agent/internal/infra/iface"
	"metric-agent/internal/model"
)

// ============ Mock 实现 ============

// mockConfigCenter 手写 ConfigCenter mock，用于测试
type mockConfigCenter struct {
	getConfigFunc      func(dataId, group string) (string, error)
	searchConfigFunc   func(group string, pageNo, pageSize int) (*model.ConfigPage, error)
	addListenerFunc    func(dataId, group string, onChange func(namespace, group, dataId, data string)) error
	cancelListenerFunc func(dataId, group string) error
	closeFunc          func() error
}

func (m *mockConfigCenter) GetConfig(dataId, group string) (string, error) {
	if m.getConfigFunc != nil {
		return m.getConfigFunc(dataId, group)
	}
	return "", nil
}
func (m *mockConfigCenter) SearchConfig(group string, pageNo, pageSize int) (*model.ConfigPage, error) {
	if m.searchConfigFunc != nil {
		return m.searchConfigFunc(group, pageNo, pageSize)
	}
	return &model.ConfigPage{}, nil
}
func (m *mockConfigCenter) AddListener(dataId, group string, onChange func(namespace, group, dataId, data string)) error {
	if m.addListenerFunc != nil {
		return m.addListenerFunc(dataId, group, onChange)
	}
	return nil
}
func (m *mockConfigCenter) CancelListener(dataId, group string) error {
	if m.cancelListenerFunc != nil {
		return m.cancelListenerFunc(dataId, group)
	}
	return nil
}
func (m *mockConfigCenter) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

// mockShellExecutor 手写 ShellExecutor mock
type mockShellExecutor struct{}

func (m *mockShellExecutor) Exec(ctx context.Context, script string, args ...string) (string, string, int, error) {
	return "", "", 0, nil
}
func (m *mockShellExecutor) ExecWithDir(ctx context.Context, workDir, script string, args ...string) (string, string, int, error) {
	return "", "", 0, nil
}

// ============ normalizeStorePath ============

func TestNormalizeStorePath(t *testing.T) {
	sep := string(os.PathSeparator)
	tests := []struct {
		input, want string
	}{
		{"/tmp/logs", "/tmp/logs" + sep},
		{"/tmp/logs/", "/tmp/logs" + sep},
		{"/tmp/logs//", "/tmp/logs" + sep},
		{"/tmp/logs\\", "/tmp/logs" + sep},
		{"tmp/logs", "tmp/logs" + sep},
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range tests {
		got := normalizeStorePath(tc.input)
		if got != tc.want {
			t.Errorf("normalizeStorePath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ============ finalNameOf ============

func TestFinalNameOf(t *testing.T) {
	tests := []struct {
		dataId, reFileName, suffix, want string
	}{
		// reFileName 优先
		{"app.log", "custom.conf", ".yml", "custom.conf"},
		{"app.log", "renamed.log", ".yml", "renamed.log"},
		// reFileName 为空 → dataId + suffix
		{"myconfig", "", ".yml", "myconfig.yml"},
		{"agent_v2", "", ".yml", "agent_v2.yml"},
		{"app.log", "", ".conf", "app.log.conf"},
		{"rules", "", ".json", "rules.json"},
		// suffix 为空 → 直接用 dataId
		{"data", "", "", "data"},
	}
	for _, tc := range tests {
		got := finalNameOf(tc.dataId, tc.reFileName, tc.suffix)
		if got != tc.want {
			t.Errorf("finalNameOf(%q, %q, %q) = %q, want %q", tc.dataId, tc.reFileName, tc.suffix, got, tc.want)
		}
	}
}

// ============ normalizeSuffix ============

func TestNormalizeSuffix(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"空字符串返回空", "", ""},
		{"纯空白返回空", "   ", ""},
		{"已有前导点不变", ".conf", ".conf"},
		{"无前导点自动补齐", "yml", ".yml"},
		{"去空白后补点", "  json  ", ".json"},
		{"已有后缀内容不变", ".yaml", ".yaml"},
	}
	for _, tc := range tests {
		got := normalizeSuffix(tc.input)
		if got != tc.want {
			t.Errorf("normalizeSuffix(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ============ normalizeCleanFixHours ============

func TestNormalizeCleanFixHours(t *testing.T) {
	tests := []struct {
		name        string
		input, want []int
	}{
		{"空", []int{}, []int{}},
		{"正常值无重复", []int{2, 5, 8}, []int{2, 5, 8}},
		{"有重复", []int{2, 2, 5}, []int{2, 5}},
		{"无效值", []int{0, 23, -1, 25}, []int{0, 23}},
		{"混合", []int{0, 5, 12, 12, 23, 7}, []int{0, 5, 7, 12, 23}},
		{"边界值0和23", []int{0, 23}, []int{0, 23}},
		{"全部无效", []int{-1, 24}, []int{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeCleanFixHours(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %v (%d), want %v (%d)", got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: got %d, want %d", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestNormalizeCleanFixHours_NoSideEffect 验证 normalizeCleanFixHours 不修改输入 slice
func TestNormalizeCleanFixHours_NoSideEffect(t *testing.T) {
	input := []int{8, 2, 5}
	original := make([]int, len(input))
	copy(original, input)
	_ = normalizeCleanFixHours(input)
	for i := range input {
		if input[i] != original[i] {
			t.Errorf("normalizeCleanFixHours 修改了输入 slice: input[%d] = %d, 原值 %d", i, input[i], original[i])
		}
	}
}

// ============ normalizeCleanSuffixes ============

func TestNormalizeCleanSuffixes(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		// 默认值填充
		{"空输入填默认", []string{}, []string{".yml", ".yaml"}},
		{"全空格填默认", []string{"", "  ", "\t"}, []string{".yml", ".yaml"}},
		// 去空、去重、小写、补点
		{"归一化", []string{"YML", "yaml", "yml", ".json", "  xml  "}, []string{".yml", ".yaml", ".json", ".xml"}},
		// 有 . 和没有 . 统一
		{"补点", []string{"conf", ".conf", "CONF"}, []string{".conf"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeCleanSuffixes(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %v (%d), want %v (%d)", got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// ============ containsInt ============

func TestContainsInt(t *testing.T) {
	list := []int{1, 5, 10}
	if !containsInt(list, 1) {
		t.Error("应包含 1")
	}
	if !containsInt(list, 10) {
		t.Error("应包含 10")
	}
	if containsInt(list, 3) {
		t.Error("不应包含 3")
	}
	if containsInt([]int{}, 1) {
		t.Error("空列表不应包含任何值")
	}
}

// ============ isHighRiskDir ============

func TestIsHighRiskDir(t *testing.T) {
	for _, dir := range myconstant.DefaultConfigCleanBlacklist {
		if !isHighRiskDir(dir) {
			t.Errorf("黑名单目录 %s 应被判定为高危", dir)
		}
		// 子路径也应被判定
		sub := dir + string(os.PathSeparator) + "subdir"
		if !isHighRiskDir(sub) {
			t.Errorf("黑名单子路径 %s 应被判定为高危", sub)
		}
	}
	if isHighRiskDir("/tmp/safe_dir") {
		t.Error("/tmp/safe_dir 不应是高危目录")
	}
}

// ============ buildFinalNameSets ============

func TestBuildFinalNameSets(t *testing.T) {
	targets := map[string]*targetConfig{
		"a": {storePath: "/a/", finalName: "x.yml"},
		"b": {storePath: "/a/", finalName: "y.yml"},
		"c": {storePath: "/b/", finalName: "z.yml"},
		"d": {storePath: "/c/", finalName: "w.yml"},
	}
	// 只收集 /a/ 和 /b/，/c/ 应被跳过
	sets := buildFinalNameSets(targets, []string{"/a/", "/b/"})
	if len(sets) != 2 {
		t.Fatalf("应有 2 个 storePath，实际 %d", len(sets))
	}
	if !sets["/a/"]["x.yml"] || !sets["/a/"]["y.yml"] {
		t.Error("/a/ 的文件名集合不完整")
	}
	if !sets["/b/"]["z.yml"] {
		t.Error("/b/ 的文件名集合不完整")
	}
	if _, ok := sets["/c/"]; ok {
		t.Error("/c/ 不应出现在结果中（不在白名单内）")
	}

	// 空白名单应返回 nil
	if sets = buildFinalNameSets(targets, nil); sets != nil {
		t.Error("空白名单应返回 nil")
	}
}

// ============ itemKey（PRD 3.2.3） ============

func TestItemKey(t *testing.T) {
	// PRD：md5(namespace + '#' + group + '#' + dataId + '#' + suffix + '#' + storePath + '#' + fileModeStr + '#' + reloadScript + '#' + reFileName)
	// 分隔符是单 #，configCode、enableClean 不参与 itemKey 生成
	// itemKeyOf 签名：(ns, group, dataId, suffix, storePath, fileModeStr, reloadScript, reFileName string)

	// 基本字段参与 itemKey 生成
	tc := &targetConfig{
		namespace:    "ns1",
		group:        "g1",
		dataId:       "d1",
		suffix:       ".yml",
		storePath:    "/a/",
		reFileName:   "renamed",
		reloadScript: "echo hi",
		fileModeStr:  "0644",
		enableClean:  true,
	}
	tc.itemKey_ = itemKeyOf(tc.namespace, tc.group, tc.dataId, tc.suffix, tc.storePath, tc.fileModeStr, tc.reloadScript, tc.reFileName)
	want := itemKeyOf("ns1", "g1", "d1", ".yml", "/a/", "0644", "echo hi", "renamed")
	got := tc.itemKey()
	if got != want {
		t.Errorf("itemKey = %q, want %q", got, want)
	}

	// suffix 不同 → itemKey 不同
	tc2 := &targetConfig{
		namespace: "ns1", group: "g1", dataId: "d1", suffix: ".json",
		storePath: "/a/", reFileName: "",
	}
	tc2.itemKey_ = itemKeyOf(tc2.namespace, tc2.group, tc2.dataId, tc2.suffix, tc2.storePath, tc2.fileModeStr, tc2.reloadScript, tc2.reFileName)
	if itemKeyOf("ns1", "g1", "d1", ".yml", "/a/", "", "", "") == tc2.itemKey() {
		t.Error("suffix 不同 → itemKey 应不同")
	}

	// reloadScript 不同 → itemKey 不同
	if itemKeyOf("ns1", "g1", "d1", ".yml", "/a/", "", "reload_a", "") == itemKeyOf("ns1", "g1", "d1", ".yml", "/a/", "", "reload_b", "") {
		t.Error("reloadScript 不同 → itemKey 应不同")
	}

	// fileModeStr 不同 → itemKey 不同
	if itemKeyOf("ns1", "g1", "d1", ".yml", "/a/", "0644", "", "") == itemKeyOf("ns1", "g1", "d1", ".yml", "/a/", "0755", "", "") {
		t.Error("fileModeStr 不同 → itemKey 应不同")
	}

	// enableClean 不参与 itemKey → 不同 enableClean 但其他字段相同 → itemKey 相同
	if itemKeyOf("ns1", "g1", "d1", ".yml", "/a/", "", "", "") != itemKeyOf("ns1", "g1", "d1", ".yml", "/a/", "", "", "") {
		t.Error("enableClean 不参与 itemKey → 即使 enableClean 不同也应相同")
	}

	// configCode 不同但其他字段完全一样 → itemKey 相同（configCode 不参与）
	k1 := itemKeyOf("ns1", "g1", "d1", ".yml", "/a/", "", "", "")
	k2 := itemKeyOf("ns1", "g1", "d1", ".yml", "/a/", "", "", "")
	if k1 != k2 {
		t.Error("仅 configCode 不同 → itemKey 应相同（configCode 不参与生成）")
	}
}

// ============ LoadConfigList（configCode 去重 + 合并 + itemKey 去重） ============

// TestLoadConfigList_BothOK_NoOverlap 个性化和公共都成功拉取、configCode 无重叠 → 合并后数量正确
func TestLoadConfigList_BothOK_NoOverlap(t *testing.T) {
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, "agent-1")
	publicDataID := myconstant.ConfigListPublicDataID

	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			if dataId == personalDataID {
				return `
- configCode: CODE_PERSONAL
  dataId: personal_only.yml
  group: AGENT_GROUP
  storePath: /etc/personal/
`, nil
			}
			if dataId == publicDataID {
				return `
- configCode: CODE_PUBLIC
  dataId: public_only.yml
  group: AGENT_GROUP
  storePath: /etc/public/
`, nil
			}
			return "", errors.New("unexpected: " + dataId)
		},
	}
	cs := newTestConfigService(cc)
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("预期成功，实际错误: %v", err)
	}
	if !result.personalOK {
		t.Error("personalOK 应为 true")
	}
	if !result.publicOK {
		t.Error("publicOK 应为 true")
	}
	if len(result.mergedConfigs) != 2 {
		t.Errorf("mergedConfigs len = %d, want 2", len(result.mergedConfigs))
	}
	if len(result.mergedTargets) != 2 {
		t.Errorf("mergedTargets len = %d, want 2", len(result.mergedTargets))
	}
}

// TestLoadConfigList_BothOK_OverlapByConfigCode 个性化和公共都成功 + 同 configCode → 只保留个性化（优先级高）
func TestLoadConfigList_BothOK_OverlapByConfigCode(t *testing.T) {
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, "agent-1")
	publicDataID := myconstant.ConfigListPublicDataID

	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			if dataId == personalDataID {
				// CODE_PERSONAL_ONLY 独有；CODE_SHARED 与公共重叠
				return `
- configCode: CODE_PERSONAL_ONLY
  dataId: personal_only.yml
  group: AGENT_GROUP
  storePath: /etc/personal/
- configCode: CODE_SHARED
  dataId: overlap_from_personal.yml
  group: AGENT_GROUP
  storePath: /etc/personal/
`, nil
			}
			if dataId == publicDataID {
				// CODE_PUBLIC_ONLY 独有；CODE_SHARED 与个性化重叠
				return `
- configCode: CODE_PUBLIC_ONLY
  dataId: public_only.yml
  group: AGENT_GROUP
  storePath: /etc/public/
- configCode: CODE_SHARED
  dataId: overlap_from_public.yml
  group: AGENT_GROUP
  storePath: /etc/public/
`, nil
			}
			return "", errors.New("unexpected: " + dataId)
		},
	}
	cs := newTestConfigService(cc)
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("预期成功，实际错误: %v", err)
	}
	// CODE_PERSONAL_ONLY + CODE_PUBLIC_ONLY + CODE_SHARED(只保留个性化) = 3 个 MetricConfig
	if len(result.mergedConfigs) != 3 {
		t.Errorf("mergedConfigs len = %d, want 3", len(result.mergedConfigs))
	}
	// CODE_SHARED 应只出现一次，source="personal"
	for _, cfg := range result.mergedConfigs {
		if cfg.ConfigCode == "CODE_SHARED" {
			if cfg.Source != "personal" {
				t.Errorf("CODE_SHARED 的 Source 应为 personal，实际 = %q", cfg.Source)
			}
			if cfg.StorePath != "/etc/personal/" {
				t.Errorf("CODE_SHARED 的 StorePath 应来自个性化 /etc/personal/，实际 = %q", cfg.StorePath)
			}
			if cfg.DataId != "overlap_from_personal.yml" {
				t.Errorf("CODE_SHARED 的 DataId 应来自个性化 overlap_from_personal.yml，实际 = %q", cfg.DataId)
			}
		}
	}
	// 展开后 3 个 targetConfig
	if len(result.mergedTargets) != 3 {
		t.Errorf("mergedTargets len = %d, want 3", len(result.mergedTargets))
	}
}

// TestLoadConfigList_PersonalOK_PublicFail 个性化成功 + 公共失败 → 只用个性化
func TestLoadConfigList_PersonalOK_PublicFail(t *testing.T) {
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, "agent-1")
	publicDataID := myconstant.ConfigListPublicDataID

	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			if dataId == personalDataID {
				return `- configCode: CODE_LOG
  dataId: mylog.yml
  group: AGENT_GROUP
  storePath: /var/log/
  enableClean: true
`, nil
			}
			if dataId == publicDataID {
				return "", errors.New("public not found")
			}
			return "", errors.New("unexpected: " + dataId)
		},
	}
	cs := newTestConfigService(cc)
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("预期成功（只有个性化），实际错误: %v", err)
	}
	if !result.personalOK {
		t.Error("personalOK 应为 true")
	}
	if result.publicOK {
		t.Error("publicOK 应为 false")
	}
	if len(result.mergedTargets) != 1 {
		t.Errorf("mergedTargets len = %d, want 1", len(result.mergedTargets))
	}
}

// TestLoadConfigList_PersonalFail_PublicOK 个性化失败 + 公共成功 → 只用公共（两者都被尝试拉取）
func TestLoadConfigList_PersonalFail_PublicOK(t *testing.T) {
	callOrder := []string{}
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, "agent-1")
	publicDataID := myconstant.ConfigListPublicDataID

	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			callOrder = append(callOrder, dataId)
			if dataId == personalDataID {
				return "", errors.New("personal not found")
			}
			if dataId == publicDataID {
				return `
- configCode: CODE_APP
  dataId: app.conf
  group: AGENT_GROUP
  storePath: /etc/app/
`, nil
			}
			return "", errors.New("unexpected dataId: " + dataId)
		},
	}
	cs := newTestConfigService(cc)
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("预期成功（只有公共），实际错误: %v", err)
	}
	if result.personalOK {
		t.Error("personalOK 应为 false")
	}
	if !result.publicOK {
		t.Error("publicOK 应为 true")
	}
	if len(result.mergedTargets) != 1 {
		t.Errorf("mergedTargets len = %d, want 1", len(result.mergedTargets))
	}
	if len(callOrder) != 2 {
		t.Errorf("两者都应该尝试拉取，应调用 2 次 GetConfig，实际 %d 次: %v", len(callOrder), callOrder)
	}
}

// TestLoadConfigList_AllFailed 两者都失败 → 返回 error
func TestLoadConfigList_AllFailed(t *testing.T) {
	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			return "", errors.New("not found")
		},
	}
	cs := newTestConfigService(cc)
	_, err := cs.LoadConfigList()
	if err == nil {
		t.Error("预期错误，实际成功")
	}
}

// TestLoadConfigList_PersonalEmpty_PublicOK 个性化空内容 + 公共成功 → 只用公共
func TestLoadConfigList_PersonalEmpty_PublicOK(t *testing.T) {
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, "agent-1")
	publicDataID := myconstant.ConfigListPublicDataID

	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			if dataId == personalDataID {
				return "", nil // 空内容视为失败
			}
			if dataId == publicDataID {
				return `- configCode: CODE_X
  dataId: x
  group: g
  storePath: /tmp/`, nil
			}
			return "", errors.New("unexpected: " + dataId)
		},
	}
	cs := newTestConfigService(cc)
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("预期成功，实际错误: %v", err)
	}
	if result.personalOK {
		t.Error("personalOK 应为 false（空内容）")
	}
	if !result.publicOK {
		t.Error("publicOK 应为 true")
	}
}

// TestLoadConfigList_PersonalEmptyArray_PublicOK 个性化空数组 + 公共成功 → 只用公共
func TestLoadConfigList_PersonalEmptyArray_PublicOK(t *testing.T) {
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, "agent-1")
	publicDataID := myconstant.ConfigListPublicDataID

	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			if dataId == personalDataID {
				return "[]", nil // 空数组视为失败
			}
			if dataId == publicDataID {
				return `- configCode: CODE_X
  dataId: x
  group: g
  storePath: /tmp/`, nil
			}
			return "", errors.New("unexpected: " + dataId)
		},
	}
	cs := newTestConfigService(cc)
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("预期成功，实际错误: %v", err)
	}
	if result.personalOK {
		t.Error("personalOK 应为 false（空数组）")
	}
	if !result.publicOK {
		t.Error("publicOK 应为 true")
	}
}

// TestLoadConfigList_InvalidYAML 两者都 YAML 无效 → 返回 error
func TestLoadConfigList_InvalidYAML(t *testing.T) {
	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			return "not: valid: yaml: [[[", nil
		},
	}
	cs := newTestConfigService(cc)
	_, err := cs.LoadConfigList()
	if err == nil {
		t.Error("预期 YAML 解析错误，实际成功")
	}
}

// TestLoadConfigList_PersonalInternalDedup 个性化内部有重复 configCode → 先配置先生效去重
func TestLoadConfigList_PersonalInternalDedup(t *testing.T) {
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, "agent-1")
	publicDataID := myconstant.ConfigListPublicDataID

	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			if dataId == personalDataID {
				return `
- configCode: DUP_CODE
  dataId: dup_first.yml
  group: AGENT_GROUP
  storePath: /first/
- configCode: DUP_CODE
  dataId: dup_second.yml
  group: AGENT_GROUP
  storePath: /second/
- configCode: UNIQUE_CODE
  dataId: unique.yml
  group: AGENT_GROUP
  storePath: /unique/
`, nil
			}
			if dataId == publicDataID {
				return `[]`, nil
			}
			return "", errors.New("unexpected: " + dataId)
		},
	}
	cs := newTestConfigService(cc)
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// 3 条 MetricConfig 中 DUP_CODE 重复 → configCode 层去重后 2 个 mergedConfigs
	if len(result.mergedConfigs) != 2 {
		t.Errorf("mergedConfigs len = %d, want 2（DUP_CODE 去重）", len(result.mergedConfigs))
	}
	// 先配置先生效 → DUP_CODE 的 StorePath 应为 /first/
	for _, cfg := range result.mergedConfigs {
		if cfg.ConfigCode == "DUP_CODE" {
			// mergedConfigs 里的 StorePath 还没 normalize（normalize 在 expand 阶段）
			if cfg.StorePath != "/first/" {
				t.Errorf("DUP_CODE 的 StorePath 应保留先配置的 /first/，实际 = %q", cfg.StorePath)
			}
			if cfg.DataId != "dup_first.yml" {
				t.Errorf("DUP_CODE 的 DataId 应保留先配置的 dup_first.yml，实际 = %q", cfg.DataId)
			}
		}
	}
}

// TestLoadConfigList_PublicInternalDedup 公共内部有重复 configCode → 先配置先生效去重
func TestLoadConfigList_PublicInternalDedup(t *testing.T) {
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, "agent-1")
	publicDataID := myconstant.ConfigListPublicDataID

	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			if dataId == personalDataID {
				return "", errors.New("personal not found")
			}
			if dataId == publicDataID {
				return `
- configCode: DUP_CODE
  dataId: dup_first.yml
  group: AGENT_GROUP
  storePath: /first/
- configCode: DUP_CODE
  dataId: dup_second.yml
  group: AGENT_GROUP
  storePath: /second/
`, nil
			}
			return "", errors.New("unexpected: " + dataId)
		},
	}
	cs := newTestConfigService(cc)
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// 2 条 MetricConfig 中 DUP_CODE 重复 → configCode 层去重后 1 个
	if len(result.mergedConfigs) != 1 {
		t.Errorf("mergedConfigs len = %d, want 1", len(result.mergedConfigs))
	}
	if result.mergedConfigs[0].StorePath != "/first/" {
		t.Errorf("DUP_CODE 的 StorePath 应保留先配置的 /first/，实际 = %q", result.mergedConfigs[0].StorePath)
	}
}

// TestLoadConfigList_ConfigCodeEmpty 所有 configCode 都为空 → validate 阶段跳过全部条目
func TestLoadConfigList_ConfigCodeEmpty(t *testing.T) {
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, "agent-1")
	publicDataID := myconstant.ConfigListPublicDataID

	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			if dataId == personalDataID {
				return `
- fileName: yolo.yml
  group: AGENT_GROUP
  storePath: /etc/yolo/
`, nil // configCode 缺失
			}
			if dataId == publicDataID {
				return `
- configCode: CODE_OK
  dataId: ok.yml
  group: AGENT_GROUP
  storePath: /etc/ok/
`, nil
			}
			return "", errors.New("unexpected: " + dataId)
		},
	}
	cs := newTestConfigService(cc)
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("预期成功，实际错误: %v", err)
	}
	// personalOK=true 但展开时那条没 configCode 被 validate 跳过；public 那条正常展开
	if len(result.mergedTargets) != 1 {
		t.Errorf("mergedTargets len = %d, want 1（无 configCode 的条目被跳过）", len(result.mergedTargets))
	}
}

// TestLoadConfigList_ConfigCodeInvalidChars configCode 含非法字符 → 该条目被跳过
func TestLoadConfigList_ConfigCodeInvalidChars(t *testing.T) {
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, "agent-1")
	publicDataID := myconstant.ConfigListPublicDataID

	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			if dataId == personalDataID {
				return `
- configCode: "BAD CODE!"
  dataId: bad.yml
  group: AGENT_GROUP
  storePath: /etc/bad/
`, nil // 空格+感叹号，非法
			}
			if dataId == publicDataID {
				return `
- configCode: CODE_OK
  dataId: ok.yml
  group: AGENT_GROUP
  storePath: /etc/ok/
`, nil
			}
			return "", errors.New("unexpected: " + dataId)
		},
	}
	cs := newTestConfigService(cc)
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("预期成功，实际错误: %v", err)
	}
	if len(result.mergedTargets) != 1 {
		t.Errorf("mergedTargets len = %d, want 1（非法 configCode 的条目被跳过）", len(result.mergedTargets))
	}
}

// ============ searchGroupDataIds ============

func TestSearchGroupDataIds_SinglePage(t *testing.T) {
	cc := &mockConfigCenter{
		searchConfigFunc: func(group string, pageNo, pageSize int) (*model.ConfigPage, error) {
			return &model.ConfigPage{
				Items: []model.ConfigItem{
					{DataId: "a.yml", Group: group},
					{DataId: "b.yml", Group: group},
					{DataId: "  ", Group: group}, // 空 dataId 跳过
				},
				TotalCount: 2,
			}, nil
		},
	}
	cs := newTestConfigService(cc)
	ids, err := cs.searchGroupDataIds("g1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "a.yml" || ids[1] != "b.yml" {
		t.Errorf("ids = %v, want [a.yml b.yml]", ids)
	}
}

func TestSearchGroupDataIds_MultiPage(t *testing.T) {
	pageCalls := 0
	totalCount := myconstant.NacosSearchPageSize + 1 // 确保会走至少两页
	cc := &mockConfigCenter{
		searchConfigFunc: func(group string, pageNo, pageSize int) (*model.ConfigPage, error) {
			pageCalls++
			if pageNo == 1 {
				items := make([]model.ConfigItem, pageSize)
				for i := range items {
					items[i] = model.ConfigItem{DataId: fmt.Sprintf("d-%03d", i), Group: group}
				}
				return &model.ConfigPage{Items: items, TotalCount: totalCount}, nil
			}
			// 第二页返回最后 1 个
			return &model.ConfigPage{
				Items:      []model.ConfigItem{{DataId: "last-one", Group: group}},
				TotalCount: totalCount,
			}, nil
		},
	}
	cs := newTestConfigService(cc)
	ids, err := cs.searchGroupDataIds("g1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if pageCalls != 2 {
		t.Errorf("SearchConfig 应被调用 2 次，实际 %d", pageCalls)
	}
	if len(ids) != totalCount {
		t.Fatalf("应拉到 %d 个 dataId，实际 %d", totalCount, len(ids))
	}
}

func TestSearchGroupDataIds_Dedup(t *testing.T) {
	cc := &mockConfigCenter{
		searchConfigFunc: func(group string, pageNo, pageSize int) (*model.ConfigPage, error) {
			return &model.ConfigPage{
				Items: []model.ConfigItem{
					{DataId: "dup.yml"},
					{DataId: "dup.yml"}, // 重复
					{DataId: "unique.yml"},
				},
				TotalCount: 3,
			}, nil
		},
	}
	cs := newTestConfigService(cc)
	ids, err := cs.searchGroupDataIds("g1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("应去重后剩 2 个，实际 %d: %v", len(ids), ids)
	}
}

func TestSearchGroupDataIds_SearchError(t *testing.T) {
	cc := &mockConfigCenter{
		searchConfigFunc: func(group string, pageNo, pageSize int) (*model.ConfigPage, error) {
			return nil, errors.New("nacos down")
		},
	}
	cs := newTestConfigService(cc)
	_, err := cs.searchGroupDataIds("g1")
	if err == nil {
		t.Error("预期错误，实际成功")
	}
}

// ============ pruneCleanLastRun ============

func TestPruneCleanLastRun(t *testing.T) {
	cs := newTestConfigService(&mockConfigCenter{})
	cs.cleanLastRun["/keep/"] = time.Now()
	cs.cleanLastRun["/drop/"] = time.Now()
	cs.cleanLastRun["/old/"] = time.Now()

	cs.pruneCleanLastRun([]string{"/keep/", "/new/"})

	if _, ok := cs.cleanLastRun["/keep/"]; !ok {
		t.Error("/keep/ 应保留")
	}
	if _, ok := cs.cleanLastRun["/drop/"]; ok {
		t.Error("/drop/ 应被删除")
	}
	if _, ok := cs.cleanLastRun["/old/"]; ok {
		t.Error("/old/ 应被删除")
	}
}

func TestPruneCleanLastRun_EmptyInput(t *testing.T) {
	cs := newTestConfigService(&mockConfigCenter{})
	cs.cleanLastRun["/a/"] = time.Now()
	cs.cleanLastRun["/b/"] = time.Now()

	cs.pruneCleanLastRun(nil)

	if len(cs.cleanLastRun) != 0 {
		t.Errorf("输入为空时应清空所有条目，实际 len=%d", len(cs.cleanLastRun))
	}
}

// ============ hasCleanSuffix ============

func TestHasCleanSuffix(t *testing.T) {
	cs := newTestConfigService(&mockConfigCenter{})

	// 默认 .yml .yaml
	if !cs.hasCleanSuffix("app.yml") {
		t.Error("app.yml 应匹配默认后缀")
	}
	if !cs.hasCleanSuffix("APP.YAML") {
		t.Error("APP.YAML 应匹配默认后缀（忽略大小写）")
	}
	if cs.hasCleanSuffix("app.conf") {
		t.Error("app.conf 不应匹配默认后缀")
	}
}

// ============ NewConfigService 默认值 ============

func TestNewConfigService_Defaults(t *testing.T) {
	cs := NewConfigService(
		&mockConfigCenter{},
		&mockShellExecutor{},
		ConfigServiceParams{
			ReloadScriptTimeout: 0,
		},
	)
	if len(cs.cleanSuffixes) != 2 {
		t.Errorf("cleanSuffixes 应默认为 2 个（.yml .yaml），实际 %v", cs.cleanSuffixes)
	}
	if cs.cleanSuffixes[0] != ".yml" || cs.cleanSuffixes[1] != ".yaml" {
		t.Errorf("cleanSuffixes = %v, want [.yml .yaml]", cs.cleanSuffixes)
	}
	if cs.params.ReloadScriptTimeout != myconstant.DefaultReloadScriptTimeout {
		t.Errorf("ReloadScriptTimeout 应默认 %d，实际 %d", myconstant.DefaultReloadScriptTimeout, cs.params.ReloadScriptTimeout)
	}
	if cs.cleanLastRun == nil {
		t.Error("cleanLastRun 应已初始化")
	}
	if cs.listenerMu == nil {
		t.Error("listenerMu 应已初始化")
	}
	if cs.registry == nil {
		t.Error("registry 应已初始化")
	}
}

// ============ StartPullLoop pullInterval 钳制 ============

func TestStartPullLoop_PullIntervalClamp(t *testing.T) {
	cs := newTestConfigService(&mockConfigCenter{})

	// 负值应被钳制
	cs.StartPullLoop(-5)
	interval := cs.pullLoopInterval()
	if interval != myconstant.DefaultPullIntervalMinutes*time.Minute {
		t.Errorf("pullInterval=-5 应钳制为默认值 %v，实际 %v", myconstant.DefaultPullIntervalMinutes*time.Minute, interval)
	}
	cs.StopPullLoop()

	// 0 应被钳制
	cs.StartPullLoop(0)
	interval = cs.pullLoopInterval()
	if interval != myconstant.DefaultPullIntervalMinutes*time.Minute {
		t.Errorf("pullInterval=0 应钳制为默认值 %v，实际 %v", myconstant.DefaultPullIntervalMinutes*time.Minute, interval)
	}
	cs.StopPullLoop()

	// 正值保持
	cs.StartPullLoop(5)
	interval = cs.pullLoopInterval()
	if interval != 5*time.Minute {
		t.Errorf("pullInterval=5 应保持为 5 分钟，实际 %v", interval)
	}
	cs.StopPullLoop()
}

// ============ 辅助 ============

func newTestConfigService(cc iface.ConfigCenter) *ConfigService {
	return NewConfigService(
		cc,
		&mockShellExecutor{},
		ConfigServiceParams{
			AgentID:             "agent-1",
			AgentGroup:          "AGENT_GROUP",
			Namespace:           "test-ns",
			NacosGroup:          "DEFAULT_GROUP",
			AgentExecDir:        "/tmp",
			ReloadScriptTimeout: 60,
			CleanOrphanFile: model.CleanOrphanFileConfig{
				Enable:       true,
				CleanFixHour: []int{2},
				CleanSuffix:  []string{".yml", ".yaml"},
			},
		},
	)
}

// pullLoopInterval 暴露 pullLoop 当前 interval（仅测试用）
func (s *ConfigService) pullLoopInterval() time.Duration {
	s.pullMu.Lock()
	defer s.pullMu.Unlock()
	return s.pullInterval
}
