package charts

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLatestChartByName(t *testing.T) {
	tests := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{
			name:    "notexist-chart",
			wantErr: true,
		},
		{
			name: "placeholder",
			want: "placeholder-v2.0.0.tgz",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := require.New(t)
			_, gotName, err := LatestChartByName(tt.name)
			if tt.wantErr {
				req.Error(err)
				return
			}
			req.NoError(err)
			req.Equal(tt.want, gotName)
		})
	}
}
