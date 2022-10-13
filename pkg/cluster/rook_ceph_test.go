package cluster

import (
	"context"
	_ "embed"
	"testing"

	"github.com/blang/semver"
	"github.com/replicatedhq/ekco/pkg/logger"
	"github.com/replicatedhq/ekco/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestParseCephOSDStatusHosts(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		count int
	}{
		{
			name: "2 unique",
			s: `
ID  HOST             USED  AVAIL  WR OPS  WR DATA  RD OPS  RD DATA  STATE
 0  areed-aka-36xk  9.77G   190G      0      123k      6      304   exists,up
 1  areed-aka-cpnv  9.77G   190G      0     2457       2      113   exists,up`,
			count: 2,
		},
		{
			name:  "0",
			s:     `ID  HOST             USED  AVAIL  WR OPS  WR DATA  RD OPS  RD DATA  STATE`,
			count: 0,
		},
		{
			name: "5 unique of 11",
			s: `
ID  HOST             USED  AVAIL  WR OPS  WR DATA  RD OPS  RD DATA  STATE
 0  areed-aka-36xk  9.77G   190G      0      123k      6      304   exists,up
 1  areed-aka-cpnv  9.77G   190G      0     2457       2      113   exists,up
 2  areed-aka-abcd  9.77G   190G      0      123k      6      304   exists,up
 3  areed-aka-abce  9.77G   190G      0      123k      6      304   exists,up
 4  areed-aka-abdf  9.77G   190G      0      123k      6      304   exists,up
 5  areed-aka-36xk  9.77G   190G      0      123k      6      304   exists,up
 6  areed-aka-cpnv  9.77G   190G      0      123k      6      304   exists,up
 7  areed-aka-abcd  9.77G   190G      0      123k      6      304   exists,up
 8  areed-aka-abce  9.77G   190G      0      123k      6      304   exists,up
 9  areed-aka-abdf  9.77G   190G      0      123k      6      304   exists,up
10  areed-aka-36xk  9.77G   190G      0      123k      6      304   exists,up`,
			count: 5,
		},
		{
			name: "Rook 1.0",
			s: `
+----+----------------+-------+-------+--------+---------+--------+---------+-----------+
| id |      host      |  used | avail | wr ops | wr data | rd ops | rd data |   state   |
+----+----------------+-------+-------+--------+---------+--------+---------+-----------+
| 0  | areed-aka-81k8 | 14.6G |  179G |    0   |     0   |    1   |     0   | exists,up |
| 1  | areed-aka-942c | 14.3G |  179G |    1   |  7372   |    1   |     0   | exists,up |
+----+----------------+-------+-------+--------+---------+--------+---------+-----------+`,
			count: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := parseCephOSDStatusHosts(test.s)
			if err != nil {
				t.Fatal(err)
			}
			count := len(actual)
			if test.count != count {
				t.Errorf("got %d, want %d", count, test.count)
			}
		})
	}
}

func TestController_SetCephCSIResources(t *testing.T) {
	configMap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rook-ceph-operator-config",
			Namespace: "rook-ceph",
		},
		Data: map[string]string{},
	}

	type args struct {
		nodeCount int
	}
	tests := []struct {
		name        string
		resources   []runtime.Object
		rookVersion semver.Version
		args        args
		want        bool
		wantErr     bool
	}{
		{
			name:        "1 node",
			resources:   []runtime.Object{configMap},
			rookVersion: semver.MustParse("1.9.12"),
			args: args{
				nodeCount: 1,
			},
			want: false,
		},
		{
			name:        "3 nodes",
			resources:   []runtime.Object{configMap},
			rookVersion: semver.MustParse("1.9.12"),
			args: args{
				nodeCount: 3,
			},
			want: true,
		},
		{
			name:        "rook 1.8",
			resources:   []runtime.Object{configMap},
			rookVersion: semver.MustParse("1.8.10"),
			args: args{
				nodeCount: 3,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(tt.resources...)
			c := &Controller{
				Config: ControllerConfig{
					Client: clientset,
				},
				Log: logger.NewDiscardLogger(),
			}
			got, err := c.SetCephCSIResources(context.Background(), tt.rookVersion, tt.args.nodeCount)
			if (err != nil) != tt.wantErr {
				t.Errorf("Controller.SetCephCSIResources() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Controller.SetCephCSIResources() = %v, want %v", got, tt.want)
			}

			got, err = c.SetCephCSIResources(context.Background(), tt.rookVersion, tt.args.nodeCount)
			if (err != nil) != tt.wantErr {
				t.Errorf("Controller.SetCephCSIResources() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != false {
				t.Errorf("Controller.SetCephCSIResources() = %v, want %v", got, false)
			}
		})
	}
}

func TestController_GetRookVersion(t *testing.T) {
	rookCephOperatorDeployment := func(image string) *appsv1.Deployment {
		return &appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rook-ceph-operator",
				Namespace: "rook-ceph",
			},
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "rook-ceph-operator",
								Image: image,
							},
						},
					},
				},
			},
		}
	}

	newSemver := func(str string) *semver.Version {
		version := semver.MustParse(str)
		return &version
	}

	tests := []struct {
		name              string
		resources         []runtime.Object
		want              *semver.Version
		wantErr           bool
		wantIsNotFoundErr bool
	}{
		{
			name: "1.9.12",
			resources: []runtime.Object{
				rookCephOperatorDeployment("rook/rook-ceph:v1.9.12"),
			},
			want: newSemver("1.9.12"),
		},
		{
			name: "1.0.4-9065b09-20210625",
			resources: []runtime.Object{
				rookCephOperatorDeployment("kurlsh/rook-ceph:v1.0.4-9065b09-20210625"),
			},
			want: newSemver("1.0.4-9065b09-20210625"),
		},
		{
			name: "invalid semver",
			resources: []runtime.Object{
				rookCephOperatorDeployment("kurlsh/rook-ceph:not-semver"),
			},
			wantErr: true,
		},
		{
			name:              "not found",
			wantErr:           true,
			wantIsNotFoundErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(tt.resources...)
			c := &Controller{
				Config: ControllerConfig{
					Client: clientset,
				},
				Log: logger.NewDiscardLogger(),
			}
			got, err := c.GetRookVersion(context.Background())
			if (err != nil) != tt.wantErr {
				t.Errorf("Controller.GetRookVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantIsNotFoundErr {
				if !util.IsNotFoundErr(err) {
					t.Errorf("Controller.GetRookVersion() error = %T, want k8serrors.IsNotFound", err)
				}
			}
			if !tt.wantErr {
				if !(*tt.want).Equals(*got) {
					t.Errorf("Controller.GetRookVersion() = %s, want %s", *got, *tt.want)
				}
			}
		})
	}
}
