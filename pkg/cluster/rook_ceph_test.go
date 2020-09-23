package cluster

import "testing"

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
