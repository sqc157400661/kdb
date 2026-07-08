package clickhouse

import (
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func buildStandaloneServices(instance *v1.KDBInstance) (*corev1.Service, *corev1.Service, error) {
	standalone, err := resolveStandaloneSpec(instance)
	if err != nil {
		return nil, nil, err
	}
	group := standalone.group
	selector := map[string]string{
		naming.LabelInstance:               instance.Name,
		naming.LabelClickHouseEngine:       naming.ClickHouseEngine,
		naming.LabelClickHouseComponent:    naming.ClickHouseComponentClickHouse,
		naming.LabelClickHouseComputeGroup: group.Name,
		naming.LabelClickHouseDataShard:    "0",
		naming.LabelClickHouseReplica:      "0",
	}
	headless := newStandaloneService(instance, naming.ClickHouseGroupHeadlessServiceName(instance.Name, group.Name), corev1.ClusterIPNone, selector)
	clientService := newStandaloneService(instance, naming.ClickHouseGroupClientServiceName(instance.Name, group.Name), "", naming.Merge(selector, map[string]string{naming.LabelClickHouseRoutable: "true"}))
	return headless, clientService, nil
}

func buildClickHouseServices(instance *v1.KDBInstance) ([]*corev1.Service, error) {
	plans, err := buildClickHouseHostPlans(instance)
	if err != nil {
		return nil, err
	}
	seen := map[string]v1.ClickHouseComputeGroupSpec{}
	for _, plan := range plans {
		seen[plan.Group.Name] = plan.Group
	}
	services := make([]*corev1.Service, 0, len(seen)*2)
	for _, group := range instance.Spec.ClickHouse.ComputeGroups {
		if _, ok := seen[group.Name]; !ok {
			continue
		}
		selector := map[string]string{
			naming.LabelInstance:               instance.Name,
			naming.LabelClickHouseEngine:       naming.ClickHouseEngine,
			naming.LabelClickHouseComponent:    naming.ClickHouseComponentClickHouse,
			naming.LabelClickHouseComputeGroup: group.Name,
		}
		clientSelector := naming.Merge(selector, map[string]string{naming.LabelClickHouseRoutable: "true"})
		services = append(services,
			newStandaloneService(instance, naming.ClickHouseGroupHeadlessServiceName(instance.Name, group.Name), corev1.ClusterIPNone, selector),
			newStandaloneService(instance, naming.ClickHouseGroupClientServiceName(instance.Name, group.Name), "", clientSelector),
		)
	}
	return services, nil
}

func newStandaloneService(instance *v1.KDBInstance, name, clusterIP string, selector map[string]string) *corev1.Service {
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Namespace:   instance.Namespace,
		Name:        name,
		Labels:      naming.Merge(instance.Labels, naming.ClickHouseLabels(instance.Name, naming.ClickHouseComponentClickHouse)),
		Annotations: instance.Annotations,
	}}
	service.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Service"))
	service.Spec = corev1.ServiceSpec{
		ClusterIP: clusterIP,
		Selector:  selector,
		Ports: []corev1.ServicePort{
			{Name: "http", Port: naming.ClickHouseHTTPPort(), TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP},
			{Name: "native", Port: naming.ClickHouseNativePort(), TargetPort: intstr.FromString("native"), Protocol: corev1.ProtocolTCP},
		},
	}
	if clusterIP == corev1.ClusterIPNone {
		service.Spec.PublishNotReadyAddresses = true
	}
	return service
}
