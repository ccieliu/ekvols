package cmd

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Short:   "Display PVC ↔ PV ↔ Volume mapping",
	Long:    `List PVC to PV mapping with capacity, storage class, volume ID, usage percentages, EBS type and node IDs.`,
	Args:    cobra.NoArgs,
	Run:     listRun,
	Example: "ekvols list",
}

func listRun(cmd *cobra.Command, args []string) {
	ctx := cmd.Context()

	// 读取命令行的 --namespace（如果未提供则为 ""，表示列出所有命名空间）
	nsFilter := ""
	if k8sFlags != nil && k8sFlags.Namespace != nil && *k8sFlags.Namespace != "" {
		nsFilter = *k8sFlags.Namespace
	}

	// ========== 1) 先建 PV 索引 ==========
	pvList, err := K8sClient.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "list PV error:", err)
		return
	}
	pvIndex := make(map[string]corev1.PersistentVolume, len(pvList.Items))
	for _, pv := range pvList.Items {
		pvIndex[pv.Name] = pv
	}

	// 收集 EBS 卷 ID（标准化为 vol-xxxx 格式）
	allVolIDs := make([]string, 0, len(pvList.Items))
	seenVol := map[string]struct{}{}
	for i := range pvList.Items {
		if id := extractVolumeID(&pvList.Items[i]); id != "" {
			if _, ok := seenVol[id]; !ok {
				seenVol[id] = struct{}{}
				allVolIDs = append(allVolIDs, id)
			}
		}
	}

	// ========== 2) kubelet /metrics，汇总 PVC 用量 & inode ==========
	type usage struct {
		usedBytes     float64
		capacityBytes float64
		inodesUsed    float64
		inodesTotal   float64
	}
	usageByPVC := map[string]*usage{} // key: ns/pvc

	nodes, err := K8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "list Nodes error:", err)
		return
	}
	// NodeName → EC2 InstanceID（从 providerID 解析）
	nodeToInstance := map[string]string{}
	for _, n := range nodes.Items {
		nodeToInstance[n.Name] = instanceIDFromProviderID(n.Spec.ProviderID)
	}

	restClient := K8sClient.CoreV1().RESTClient()
	parser := expfmt.TextParser{}
	parseFamilies := func(raw []byte) map[string]*dto.MetricFamily {
		fams, err := parser.TextToMetricFamilies(bytes.NewReader(raw))
		if err != nil {
			return nil
		}
		return fams
	}
	for _, n := range nodes.Items {
		raw, err := restClient.Get().AbsPath("/api/v1/nodes/" + n.Name + "/proxy/metrics").Do(ctx).Raw()
		if err != nil {
			continue
		}
		fams := parseFamilies(raw)
		if fams == nil || fams["kubelet_volume_stats_used_bytes"] == nil {
			raw2, err2 := restClient.Get().AbsPath("/api/v1/nodes/" + n.Name + "/proxy/metrics/resource").Do(ctx).Raw()
			if err2 == nil {
				if fams2 := parseFamilies(raw2); fams2 != nil {
					fams = fams2
				}
			}
		}
		if fams == nil {
			continue
	}
		for _, name := range []string{
			"kubelet_volume_stats_used_bytes",
			"kubelet_volume_stats_capacity_bytes",
			"kubelet_volume_stats_inodes",
			"kubelet_volume_stats_inodes_used",
		} {
			mf := fams[name]
			if mf == nil {
				continue
			}
			for _, m := range mf.Metric {
				var ns, pvc string
				for _, lp := range m.Label {
					switch lp.GetName() {
					case "namespace":
						ns = lp.GetValue()
					case "persistentvolumeclaim":
						pvc = lp.GetValue()
					}
				}
				// 只聚合目标命名空间（如果设置了 -n）
				if ns == "" || pvc == "" || (nsFilter != "" && ns != nsFilter) {
					continue
				}
				key := ns + "/" + pvc
				u := usageByPVC[key]
				if u == nil {
					u = &usage{}
					usageByPVC[key] = u
				}
				val := 0.0
				if m.Gauge != nil && m.Gauge.Value != nil {
					val = m.Gauge.GetValue()
				} else if m.Untyped != nil && m.Untyped.Value != nil {
					val = m.Untyped.GetValue()
				}
				switch name {
				case "kubelet_volume_stats_used_bytes":
					u.usedBytes = val
				case "kubelet_volume_stats_capacity_bytes":
					u.capacityBytes = val
				case "kubelet_volume_stats_inodes":
					u.inodesTotal = val
				case "kubelet_volume_stats_inodes_used":
					u.inodesUsed = val
				}
			}
		}
	}

	// ========== 3) 解析 Pod → PVC → Node 映射（用于 NODE_ID 列；按 namespace 过滤）==========
	pvcToNodes := map[string]map[string]struct{}{} // key: ns/pvc → set(nodeName)
	podNS := nsFilter // 若为空则列出全部
	pods, err := K8sClient.CoreV1().Pods(podNS).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, p := range pods.Items {
			if p.Spec.NodeName == "" {
				continue
			}
			for _, v := range p.Spec.Volumes {
				if v.PersistentVolumeClaim == nil {
					continue
				}
				k := p.Namespace + "/" + v.PersistentVolumeClaim.ClaimName
				if pvcToNodes[k] == nil {
					pvcToNodes[k] = map[string]struct{}{}
				}
				pvcToNodes[k][p.Spec.NodeName] = struct{}{}
			}
		}
	}

	// ========== 4) DescribeVolumes 获取 VTYPE ==========
	volType := map[string]string{} // vol-xxx → type
	if AwsClient != nil && AwsClient.EC2 != nil && len(allVolIDs) > 0 {
		const batch = 200
		for i := 0; i < len(allVolIDs); i += batch {
			end := i + batch
			if end > len(allVolIDs) {
				end = len(allVolIDs)
			}
			input := &ec2.DescribeVolumesInput{VolumeIds: allVolIDs[i:end]}
			out, err := AwsClient.EC2.DescribeVolumes(ctx, input)
			if err != nil {
				// 失败不致命，保持空白
				continue
			}
			for _, v := range out.Volumes {
				if v.VolumeId == nil || v.VolumeType == "" {
					continue
				}
				volType[*v.VolumeId] = string(v.VolumeType)
			}
		}
	}

	// ========== 5) 遍历 PVC（按 namespace 过滤），打印表格 ==========
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAMESPACE\tPVC\tPV\tCAP\tSC\tVOLUME_ID\tVTYPE\tNODE_ID\tSTATUS\tCAP%\tIND%\tAM\tRC\tAGE")

	pvcNS := nsFilter // 若为空则列出全部
	pvcList, err := K8sClient.CoreV1().PersistentVolumeClaims(pvcNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "list PVC error:", err)
		return
	}

	now := time.Now()

	for _, pvc := range pvcList.Items {
		pvName := pvc.Spec.VolumeName

		// 容量（Requests）
		capStr := "-"
		if qty, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			capStr = qty.String()
		}

		// StorageClass
		sc := ""
		if pvc.Spec.StorageClassName != nil {
			sc = *pvc.Spec.StorageClassName
		}
		pvObj, hasPV := pvIndex[pvName]
		if sc == "" && hasPV && pvObj.Spec.StorageClassName != "" {
			sc = pvObj.Spec.StorageClassName
		}

		// VolumeID（CSI 优先；兼容 in-tree）
		volID := ""
		if hasPV {
			if pvObj.Spec.CSI != nil && pvObj.Spec.CSI.VolumeHandle != "" {
				volID = normalizeVolID(pvObj.Spec.CSI.VolumeHandle)
			} else if pvObj.Spec.AWSElasticBlockStore != nil {
				volID = normalizeVolID(pvObj.Spec.AWSElasticBlockStore.VolumeID)
			}
		}

		// VTYPE
		vtype := "-"
		if volID != "" {
			if t, ok := volType[volID]; ok && t != "" {
				vtype = t
			}
		}

		// NODE_ID（可能有多个，逗号分隔）
		key := pvc.Namespace + "/" + pvc.Name
		nodeIDs := "-"
		if nodesSet, ok := pvcToNodes[key]; ok && len(nodesSet) > 0 {
			ids := make([]string, 0, len(nodesSet))
			for nodeName := range nodesSet {
				if iid := nodeToInstance[nodeName]; iid != "" {
					ids = append(ids, iid)
				}
			}
			if len(ids) > 0 {
				nodeIDs = strings.Join(ids, ",")
			}
		}

		// 百分比（保留 1 位小数）
		capPct, inodePct := "-", "-"
		if u := usageByPVC[key]; u != nil {
			if u.capacityBytes > 0 {
				pct := float64(u.usedBytes) / float64(u.capacityBytes) * 100.0
				capPct = fmt.Sprintf("%.1f", pct)
			}
			if u.inodesTotal > 0 {
				pct := float64(u.inodesUsed) / float64(u.inodesTotal) * 100.0
				inodePct = fmt.Sprintf("%.1f", pct)
			}
		}

		// 访问模式（RWO/ROX/RWX/RWOP）
		access := accessModesShort(pvc.Spec.AccessModes)

		// 回收策略
		reclaim := "-"
		if hasPV {
			reclaim = string(pvObj.Spec.PersistentVolumeReclaimPolicy)
			if reclaim == "" {
				reclaim = "-"
			}
		}

		// 年龄
		age := humanizeAge(now.Sub(pvc.CreationTimestamp.Time))

		if hasPV {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				pvc.Namespace, pvc.Name, pvObj.Name, capStr, sc, volID, vtype, nodeIDs, pvc.Status.Phase, capPct, inodePct, access, reclaim, age)
		} else {
			fmt.Fprintf(w, "%s\t%s\t(none)\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				pvc.Namespace, pvc.Name, capStr, sc, volID, vtype, nodeIDs, pvc.Status.Phase, capPct, inodePct, access, reclaim, age)
		}
	}
	w.Flush()
}

func init() {
	rootCmd.AddCommand(listCmd)
}

// --- helpers ---

// 从 PV 中抽取并标准化 EBS 卷 ID（返回 vol-xxxx）
func extractVolumeID(pv *corev1.PersistentVolume) string {
	if pv == nil {
		return ""
	}
	if pv.Spec.CSI != nil && pv.Spec.CSI.VolumeHandle != "" {
		return normalizeVolID(pv.Spec.CSI.VolumeHandle)
	}
	if pv.Spec.AWSElasticBlockStore != nil && pv.Spec.AWSElasticBlockStore.VolumeID != "" {
		return normalizeVolID(pv.Spec.AWSElasticBlockStore.VolumeID)
	}
	return ""
}

var volRe = regexp.MustCompile(`vol-[0-9a-fA-F]+`)

func normalizeVolID(s string) string {
	if s == "" {
		return ""
	}
	if m := volRe.FindString(s); m != "" {
		return m
	}
	return s
}

// providerID 形如 "aws:///eu-west-1a/i-0123456789abcdef0" → "i-0123456789abcdef0"
func instanceIDFromProviderID(pid string) string {
	if pid == "" {
		return ""
	}
	parts := strings.Split(pid, "/")
	return parts[len(parts)-1]
}

func accessModesShort(modes []corev1.PersistentVolumeAccessMode) string {
	if len(modes) == 0 {
		return "-"
	}
	abbr := make([]string, 0, len(modes))
	for _, m := range modes {
		switch m {
		case corev1.ReadWriteOnce:
			abbr = append(abbr, "RWO")
		case corev1.ReadOnlyMany:
			abbr = append(abbr, "ROX")
		case corev1.ReadWriteMany:
			abbr = append(abbr, "RWX")
		case corev1.ReadWriteOncePod:
			abbr = append(abbr, "RWOP")
		default:
			abbr = append(abbr, string(m))
		}
	}
	return strings.Join(abbr, ",")
}

func humanizeAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}
