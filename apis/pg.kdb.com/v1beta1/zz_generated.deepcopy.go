//go:build !ignore_autogenerated
// +build !ignore_autogenerated

/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Code generated by controller-gen. DO NOT EDIT.

package v1beta1

import (
	"github.com/sqc157400661/kdb/apis/shared"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Backups) DeepCopyInto(out *Backups) {
	*out = *in
	in.PGBackRest.DeepCopyInto(&out.PGBackRest)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Backups.
func (in *Backups) DeepCopy() *Backups {
	if in == nil {
		return nil
	}
	out := new(Backups)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MonitoringStatus) DeepCopyInto(out *MonitoringStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MonitoringStatus.
func (in *MonitoringStatus) DeepCopy() *MonitoringStatus {
	if in == nil {
		return nil
	}
	out := new(MonitoringStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PGBackRestArchive) DeepCopyInto(out *PGBackRestArchive) {
	*out = *in
	if in.Metadata != nil {
		in, out := &in.Metadata, &out.Metadata
		*out = new(shared.Metadata)
		(*in).DeepCopyInto(*out)
	}
	if in.Global != nil {
		in, out := &in.Global, &out.Global
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Restore != nil {
		in, out := &in.Restore, &out.Restore
		*out = new(PGBackRestRestore)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PGBackRestArchive.
func (in *PGBackRestArchive) DeepCopy() *PGBackRestArchive {
	if in == nil {
		return nil
	}
	out := new(PGBackRestArchive)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PGBackRestJobStatus) DeepCopyInto(out *PGBackRestJobStatus) {
	*out = *in
	if in.StartTime != nil {
		in, out := &in.StartTime, &out.StartTime
		*out = (*in).DeepCopy()
	}
	if in.CompletionTime != nil {
		in, out := &in.CompletionTime, &out.CompletionTime
		*out = (*in).DeepCopy()
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PGBackRestJobStatus.
func (in *PGBackRestJobStatus) DeepCopy() *PGBackRestJobStatus {
	if in == nil {
		return nil
	}
	out := new(PGBackRestJobStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PGBackRestRepo) DeepCopyInto(out *PGBackRestRepo) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PGBackRestRepo.
func (in *PGBackRestRepo) DeepCopy() *PGBackRestRepo {
	if in == nil {
		return nil
	}
	out := new(PGBackRestRepo)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PGBackRestRestore) DeepCopyInto(out *PGBackRestRestore) {
	*out = *in
	if in.Enabled != nil {
		in, out := &in.Enabled, &out.Enabled
		*out = new(bool)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PGBackRestRestore.
func (in *PGBackRestRestore) DeepCopy() *PGBackRestRestore {
	if in == nil {
		return nil
	}
	out := new(PGBackRestRestore)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PGBackRestStatus) DeepCopyInto(out *PGBackRestStatus) {
	*out = *in
	if in.RepoHost != nil {
		in, out := &in.RepoHost, &out.RepoHost
		*out = new(RepoHostStatus)
		**out = **in
	}
	if in.Repos != nil {
		in, out := &in.Repos, &out.Repos
		*out = make([]RepoStatus, len(*in))
		copy(*out, *in)
	}
	if in.Restore != nil {
		in, out := &in.Restore, &out.Restore
		*out = new(PGBackRestJobStatus)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PGBackRestStatus.
func (in *PGBackRestStatus) DeepCopy() *PGBackRestStatus {
	if in == nil {
		return nil
	}
	out := new(PGBackRestStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PGBouncer) DeepCopyInto(out *PGBouncer) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PGBouncer.
func (in *PGBouncer) DeepCopy() *PGBouncer {
	if in == nil {
		return nil
	}
	out := new(PGBouncer)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PGBouncer) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PGBouncerConfiguration) DeepCopyInto(out *PGBouncerConfiguration) {
	*out = *in
	if in.Files != nil {
		in, out := &in.Files, &out.Files
		*out = make([]v1.VolumeProjection, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Global != nil {
		in, out := &in.Global, &out.Global
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Databases != nil {
		in, out := &in.Databases, &out.Databases
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Users != nil {
		in, out := &in.Users, &out.Users
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PGBouncerConfiguration.
func (in *PGBouncerConfiguration) DeepCopy() *PGBouncerConfiguration {
	if in == nil {
		return nil
	}
	out := new(PGBouncerConfiguration)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PGBouncerList) DeepCopyInto(out *PGBouncerList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PGBouncerList.
func (in *PGBouncerList) DeepCopy() *PGBouncerList {
	if in == nil {
		return nil
	}
	out := new(PGBouncerList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PGBouncerList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PGBouncerPodSpec) DeepCopyInto(out *PGBouncerPodSpec) {
	*out = *in
	if in.Metadata != nil {
		in, out := &in.Metadata, &out.Metadata
		*out = new(shared.Metadata)
		(*in).DeepCopyInto(*out)
	}
	if in.Affinity != nil {
		in, out := &in.Affinity, &out.Affinity
		*out = new(v1.Affinity)
		(*in).DeepCopyInto(*out)
	}
	in.Config.DeepCopyInto(&out.Config)
	if in.Containers != nil {
		in, out := &in.Containers, &out.Containers
		*out = make([]v1.Container, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Port != nil {
		in, out := &in.Port, &out.Port
		*out = new(int32)
		**out = **in
	}
	if in.PriorityClassName != nil {
		in, out := &in.PriorityClassName, &out.PriorityClassName
		*out = new(string)
		**out = **in
	}
	if in.Replicas != nil {
		in, out := &in.Replicas, &out.Replicas
		*out = new(int32)
		**out = **in
	}
	if in.DeployInSidecar != nil {
		in, out := &in.DeployInSidecar, &out.DeployInSidecar
		*out = new(bool)
		**out = **in
	}
	if in.RuntimeClassName != nil {
		in, out := &in.RuntimeClassName, &out.RuntimeClassName
		*out = new(string)
		**out = **in
	}
	if in.MinAvailable != nil {
		in, out := &in.MinAvailable, &out.MinAvailable
		*out = new(intstr.IntOrString)
		**out = **in
	}
	in.Resources.DeepCopyInto(&out.Resources)
	if in.Service != nil {
		in, out := &in.Service, &out.Service
		*out = new(shared.ServiceSpec)
		(*in).DeepCopyInto(*out)
	}
	if in.Tolerations != nil {
		in, out := &in.Tolerations, &out.Tolerations
		*out = make([]v1.Toleration, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.TopologySpreadConstraints != nil {
		in, out := &in.TopologySpreadConstraints, &out.TopologySpreadConstraints
		*out = make([]v1.TopologySpreadConstraint, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PGBouncerPodSpec.
func (in *PGBouncerPodSpec) DeepCopy() *PGBouncerPodSpec {
	if in == nil {
		return nil
	}
	out := new(PGBouncerPodSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PGBouncerPodStatus) DeepCopyInto(out *PGBouncerPodStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PGBouncerPodStatus.
func (in *PGBouncerPodStatus) DeepCopy() *PGBouncerPodStatus {
	if in == nil {
		return nil
	}
	out := new(PGBouncerPodStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PatroniSpec) DeepCopyInto(out *PatroniSpec) {
	*out = *in
	in.DynamicConfiguration.DeepCopyInto(&out.DynamicConfiguration)
	if in.LeaderLeaseDurationSeconds != nil {
		in, out := &in.LeaderLeaseDurationSeconds, &out.LeaderLeaseDurationSeconds
		*out = new(int32)
		**out = **in
	}
	if in.Port != nil {
		in, out := &in.Port, &out.Port
		*out = new(int32)
		**out = **in
	}
	if in.SyncPeriodSeconds != nil {
		in, out := &in.SyncPeriodSeconds, &out.SyncPeriodSeconds
		*out = new(int32)
		**out = **in
	}
	if in.Switchover != nil {
		in, out := &in.Switchover, &out.Switchover
		*out = new(PatroniSwitchover)
		(*in).DeepCopyInto(*out)
	}
	if in.DisableFailover != nil {
		in, out := &in.DisableFailover, &out.DisableFailover
		*out = new(bool)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PatroniSpec.
func (in *PatroniSpec) DeepCopy() *PatroniSpec {
	if in == nil {
		return nil
	}
	out := new(PatroniSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PatroniStatus) DeepCopyInto(out *PatroniStatus) {
	*out = *in
	if in.Switchover != nil {
		in, out := &in.Switchover, &out.Switchover
		*out = new(string)
		**out = **in
	}
	if in.SwitchoverTimeline != nil {
		in, out := &in.SwitchoverTimeline, &out.SwitchoverTimeline
		*out = new(int64)
		**out = **in
	}
	if in.DisableFailover != nil {
		in, out := &in.DisableFailover, &out.DisableFailover
		*out = new(bool)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PatroniStatus.
func (in *PatroniStatus) DeepCopy() *PatroniStatus {
	if in == nil {
		return nil
	}
	out := new(PatroniStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PatroniSwitchover) DeepCopyInto(out *PatroniSwitchover) {
	*out = *in
	if in.TargetInstance != nil {
		in, out := &in.TargetInstance, &out.TargetInstance
		*out = new(string)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PatroniSwitchover.
func (in *PatroniSwitchover) DeepCopy() *PatroniSwitchover {
	if in == nil {
		return nil
	}
	out := new(PatroniSwitchover)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PostgresInstance) DeepCopyInto(out *PostgresInstance) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PostgresInstance.
func (in *PostgresInstance) DeepCopy() *PostgresInstance {
	if in == nil {
		return nil
	}
	out := new(PostgresInstance)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PostgresInstance) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PostgresInstanceList) DeepCopyInto(out *PostgresInstanceList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]PostgresInstance, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PostgresInstanceList.
func (in *PostgresInstanceList) DeepCopy() *PostgresInstanceList {
	if in == nil {
		return nil
	}
	out := new(PostgresInstanceList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PostgresInstanceList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PostgresInstanceSpec) DeepCopyInto(out *PostgresInstanceSpec) {
	*out = *in
	in.Backups.DeepCopyInto(&out.Backups)
	in.InstanceSet.DeepCopyInto(&out.InstanceSet)
	if in.Patroni != nil {
		in, out := &in.Patroni, &out.Patroni
		*out = new(PatroniSpec)
		(*in).DeepCopyInto(*out)
	}
	if in.Port != nil {
		in, out := &in.Port, &out.Port
		*out = new(int32)
		**out = **in
	}
	if in.Proxy != nil {
		in, out := &in.Proxy, &out.Proxy
		*out = new(PostgresProxySpec)
		(*in).DeepCopyInto(*out)
	}
	if in.Service != nil {
		in, out := &in.Service, &out.Service
		*out = new(shared.ServiceSpec)
		(*in).DeepCopyInto(*out)
	}
	if in.Shutdown != nil {
		in, out := &in.Shutdown, &out.Shutdown
		*out = new(bool)
		**out = **in
	}
	if in.SupplementalGroups != nil {
		in, out := &in.SupplementalGroups, &out.SupplementalGroups
		*out = make([]int64, len(*in))
		copy(*out, *in)
	}
	if in.Users != nil {
		in, out := &in.Users, &out.Users
		*out = make([]PostgresUserSpec, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Config != nil {
		in, out := &in.Config, &out.Config
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PostgresInstanceSpec.
func (in *PostgresInstanceSpec) DeepCopy() *PostgresInstanceSpec {
	if in == nil {
		return nil
	}
	out := new(PostgresInstanceSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PostgresInstanceStatus) DeepCopyInto(out *PostgresInstanceStatus) {
	*out = *in
	in.InstanceSet.DeepCopyInto(&out.InstanceSet)
	in.Patroni.DeepCopyInto(&out.Patroni)
	if in.PGBackRest != nil {
		in, out := &in.PGBackRest, &out.PGBackRest
		*out = new(PGBackRestStatus)
		(*in).DeepCopyInto(*out)
	}
	out.Proxy = in.Proxy
	out.Monitoring = in.Monitoring
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PostgresInstanceStatus.
func (in *PostgresInstanceStatus) DeepCopy() *PostgresInstanceStatus {
	if in == nil {
		return nil
	}
	out := new(PostgresInstanceStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PostgresProxySpec) DeepCopyInto(out *PostgresProxySpec) {
	*out = *in
	if in.PGBouncer != nil {
		in, out := &in.PGBouncer, &out.PGBouncer
		*out = new(PGBouncerPodSpec)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PostgresProxySpec.
func (in *PostgresProxySpec) DeepCopy() *PostgresProxySpec {
	if in == nil {
		return nil
	}
	out := new(PostgresProxySpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PostgresProxyStatus) DeepCopyInto(out *PostgresProxyStatus) {
	*out = *in
	out.PGBouncer = in.PGBouncer
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PostgresProxyStatus.
func (in *PostgresProxyStatus) DeepCopy() *PostgresProxyStatus {
	if in == nil {
		return nil
	}
	out := new(PostgresProxyStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PostgresUserSpec) DeepCopyInto(out *PostgresUserSpec) {
	*out = *in
	if in.Databases != nil {
		in, out := &in.Databases, &out.Databases
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PostgresUserSpec.
func (in *PostgresUserSpec) DeepCopy() *PostgresUserSpec {
	if in == nil {
		return nil
	}
	out := new(PostgresUserSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RepoHostStatus) DeepCopyInto(out *RepoHostStatus) {
	*out = *in
	out.TypeMeta = in.TypeMeta
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RepoHostStatus.
func (in *RepoHostStatus) DeepCopy() *RepoHostStatus {
	if in == nil {
		return nil
	}
	out := new(RepoHostStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RepoStatus) DeepCopyInto(out *RepoStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RepoStatus.
func (in *RepoStatus) DeepCopy() *RepoStatus {
	if in == nil {
		return nil
	}
	out := new(RepoStatus)
	in.DeepCopyInto(out)
	return out
}