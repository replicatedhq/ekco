package cluster

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_shouldRestartEnvoyPod(t *testing.T) {
	tests := []struct {
		name      string
		want      bool
		podStatus corev1.PodStatus
	}{
		{
			name: "delete me",
			want: true,
			podStatus: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:  "shutdown-manager",
						Ready: true,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: metav1.Time{Time: time.Now().Add(-time.Hour)},
							},
						},
					},
					{
						Name:  "envoy",
						Ready: false,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: metav1.Time{Time: time.Now().Add(-time.Hour)},
							},
						},
					},
				},
				Conditions: []corev1.PodCondition{{
					Type:               corev1.ContainersReady,
					Status:             corev1.ConditionFalse,
					Reason:             "ContainersNotReady",
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-2 * time.Minute)},
				}},
			},
		},
		{
			name: "hasnt been enough time",
			want: false,
			podStatus: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:  "shutdown-manager",
						Ready: true,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: metav1.Time{Time: time.Now().Add(-time.Hour)},
							},
						},
					},
					{
						Name:  "envoy",
						Ready: false,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: metav1.Time{Time: time.Now().Add(-time.Hour)},
							},
						},
					},
				},
				Conditions: []corev1.PodCondition{{
					Type:               corev1.ContainersReady,
					Status:             corev1.ConditionFalse,
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-time.Second)},
				}},
			},
		},
		{
			name: "healthy",
			want: false,
			podStatus: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:  "shutdown-manager",
						Ready: true,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: metav1.Time{Time: time.Now().Add(-time.Hour)},
							},
						},
					},
					{
						Name:  "envoy",
						Ready: true,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: metav1.Time{Time: time.Now().Add(-time.Hour)},
							},
						},
					},
				},
				Conditions: []corev1.PodCondition{{
					Type:               corev1.ContainersReady,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-time.Hour)},
				}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRestartEnvoyPod(tt.podStatus, time.Minute); got != tt.want {
				t.Errorf("shouldRestartEnvoyPod() = %v, want %v", got, tt.want)
			}
		})
	}
}
