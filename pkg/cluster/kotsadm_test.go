package cluster

import (
	"context"
	"reflect"
	"testing"

	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/pointer"
)

func TestEnableHAKotsadm(t *testing.T) {
	type args struct {
		ctx       context.Context
		clientset kubernetes.Interface
		namespace string
	}
	tests := []struct {
		name         string
		args         args
		wantReplicas int32
		wantArgs     []string
		wantErr      bool
	}{
		{
			name: "scales up rqlite and modifies its args",
			args: args{
				ctx:       context.Background(),
				namespace: "default",
				clientset: fake.NewSimpleClientset(&appsv1.StatefulSetList{
					Items: []appsv1.StatefulSet{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "kotsadm-rqlite",
								Namespace: "default",
							},
							Spec: appsv1.StatefulSetSpec{
								Replicas: pointer.Int32Ptr(1),
								Template: corev1.PodTemplateSpec{
									Spec: corev1.PodSpec{
										Containers: []corev1.Container{
											{
												Name: "rqlite",
												Args: []string{
													"-disco-mode=dns",
													"-disco-config={\"name\":\"kotsadm-rqlite-headless\"}",
													"-bootstrap-expect=1",
													"-auth=/auth/config.json",
													"-join-as=kotsadm",
												},
											},
										},
									},
								},
							},
						},
					},
				}),
			},
			wantReplicas: 3,
			wantArgs: []string{
				"-disco-mode=dns",
				"-disco-config={\"name\":\"kotsadm-rqlite-headless\"}",
				"-bootstrap-expect=3",
				"-auth=/auth/config.json",
				"-join-as=kotsadm",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, _ := zap.NewProduction()
			c := NewController(ControllerConfig{
				Client: tt.args.clientset,
			}, logger.Sugar())

			err := c.EnableHAKotsadm(tt.args.ctx, tt.args.namespace)
			if (err != nil) != tt.wantErr {
				t.Errorf("EnableHAKotsadm() error = %v, wantErr: %v", err, tt.wantErr)
				return
			}

			sts, err := tt.args.clientset.AppsV1().StatefulSets(tt.args.namespace).Get(tt.args.ctx, "kotsadm-rqlite", metav1.GetOptions{})
			if err != nil {
				t.Errorf("failed to get kotsadm-rqlite statefulset: %v", err)
			}
			if *sts.Spec.Replicas != tt.wantReplicas {
				t.Errorf("replicas = %v, want: %v", *sts.Spec.Replicas, tt.wantReplicas)
			}
			if !reflect.DeepEqual(sts.Spec.Template.Spec.Containers[0].Args, tt.wantArgs) {
				t.Errorf("args = %v, want: %v", sts.Spec.Template.Spec.Containers[0].Args, tt.wantArgs)
			}
		})
	}
}
