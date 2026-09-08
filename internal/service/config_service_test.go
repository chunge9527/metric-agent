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
		dataId, reFileName, want string
	}{
		// reFileName 优先
		{"app.log.yml", "custom.conf", "custom.conf"},
		{"app.log", "renamed.log", "renamed.log"},
		// dataId 命中已知后缀 → 直接用 dataId
		{"app.log.yml", "", "app.log.yml"},
		{"config.yml", "", "config.yml"},
		{"rules.json", "", "rules.json"},
		{"settings.xml", "", "settings.xml"},
		{"page.html", "", "page.html"},
		{"data.properties", "", "data.properties"},
		{"notes.txt", "", "notes.txt"},
		// 未命中 → 补 .yml
		{"myconfig", "", "myconfig.yml"},
		{"agent_v2", "", "agent_v2.yml"},
		// 大小写
		{"CONFIG.YML", "", "CONFIG.YML"},
		{"Config.YAML", "", "Config.YAML"},
	}
	for _, tc := range tests {
		got := finalNameOf(tc.dataId, tc.reFileName)
		if got != tc.want {
			t.Errorf("finalNameOf(%q, %q) = %q, want %q", tc.dataId, tc.reFileName, got, tc.want)
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
	}
	sets := buildFinalNameSets(targets)
	if len(sets) != 2 {
		t.Fatalf("应有 2 个 storePath，实际 %d", len(sets))
	}
	if !sets["/a/"]["x.yml"] || !sets["/a/"]["y.yml"] {
		t.Error("/a/ 的文件名集合不完整")
	}
	if !sets["/b/"]["z.yml"] {
		t.Error("/b/ 的文件名集合不完整")
	}
}

// ============ targetConfig.groupKey ============

func TestGroupKey(t *testing.T) {
	tc := &targetConfig{namespace: "ns1", group: "g1", dataId: "d1"}
	want := "ns1##g1##d1"
	if tc.groupKey() != want {
		t.Errorf("groupKey = %q, want %q", tc.groupKey(), want)
	}
}

// ============ LoadConfigList / tryParseList ============

func TestLoadConfigList_SuccessPersonal(t *testing.T) {
	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			return `
- fileName: mylog.yml
  group: AGENT_GROUP
  storePath: /var/log/
  enableClean: true
`, nil
		},
	}
	cs := newTestConfigService(cc)
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("预期成功，实际错误: %v", err)
	}
	if result.source != "personal" {
		t.Errorf("source = %q, want personal", result.source)
	}
	if len(result.configs) != 1 {
		t.Errorf("configs len = %d, want 1", len(result.configs))
	}
	if result.configs[0].FileName != "mylog.yml" {
		t.Errorf("fileName = %q, want mylog.yml", result.configs[0].FileName)
	}
}

func TestLoadConfigList_PersonalFallbackToPublic(t *testing.T) {
	callOrder := []string{}
	// 个性化 dataId 末尾带 _agent-1，公共的不带
	cs := newTestConfigService(nil)
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, "AGENT_GROUP", "agent-1")
	publicDataID := fmt.Sprintf(myconstant.ConfigListPublicDataIDFormat, "AGENT_GROUP")

	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			callOrder = append(callOrder, dataId)
			if dataId == personalDataID {
				return "", errors.New("personal not found")
			}
			if dataId == publicDataID {
				return `
- fileName: app.conf
  group: AGENT_GROUP
  storePath: /etc/app/
`, nil
			}
			return "", errors.New("unexpected dataId: " + dataId)
		},
	}
	cs.cc = cc
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("预期成功降级，实际错误: %v", err)
	}
	if result.source != "public" {
		t.Errorf("source = %q, want public", result.source)
	}
	if len(callOrder) != 2 {
		t.Errorf("应调用 2 次 GetConfig，实际 %d 次: %v", len(callOrder), callOrder)
	}
}

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

func TestLoadConfigList_EmptyContentFallback(t *testing.T) {
	cs := newTestConfigService(nil)
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, "AGENT_GROUP", "agent-1")

	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			if dataId == personalDataID {
				return "", nil // 空内容视为失败
			}
			return `- fileName: x
  group: g
  storePath: /tmp/`, nil
		},
	}
	cs.cc = cc
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("预期降级成功，实际错误: %v", err)
	}
	if result.source != "public" {
		t.Errorf("source = %q, want public", result.source)
	}
}

func TestLoadConfigList_EmptyArrayFallback(t *testing.T) {
	cs := newTestConfigService(nil)
	personalDataID := fmt.Sprintf(myconstant.ConfigListPersonalDataIDFormat, "AGENT_GROUP", "agent-1")

	cc := &mockConfigCenter{
		getConfigFunc: func(dataId, group string) (string, error) {
			if dataId == personalDataID {
				return "[]", nil // 空数组视为失败
			}
			return `- fileName: x
  group: g
  storePath: /tmp/`, nil
		},
	}
	cs.cc = cc
	result, err := cs.LoadConfigList()
	if err != nil {
		t.Fatalf("预期降级成功，实际错误: %v", err)
	}
	if result.source != "public" {
		t.Errorf("source = %q, want public", result.source)
	}
}

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
