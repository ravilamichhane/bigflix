package web

import "testing"

func TestScaledHeight(t *testing.T) {
	tests := []struct {
		name             string
		srcW, srcH, maxH int
		want             int
	}{
		{"4K 16:9 stays 2160p", 3840, 2160, 2160, 2160},
		{"4K 16:9 to 1440p", 3840, 2160, 1440, 1440},
		{"4K 16:9 to 1080p", 3840, 2160, 1080, 1080},
		{"4K 2.39:1 ultrawide to 1080p", 3840, 1600, 1080, 800},
		{"4K 16:9 to 720p", 3840, 2160, 720, 720},
		{"4K 2.39:1 ultrawide to 720p", 3840, 1600, 720, 532},
		{"1080p to 480p", 1920, 1080, 480, 480},
		{"1080p ultrawide does not upscale", 1920, 800, 1080, 800},
		{"720p does not upscale to 1080p", 1280, 720, 1080, 720},
		{"zero maxH preserves height", 3840, 2160, 0, 2160},
		{"unknown source falls back to maxH", 0, 0, 1080, 1080},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := scaledHeight(tc.srcW, tc.srcH, tc.maxH); got != tc.want {
				t.Fatalf("scaledHeight(%d, %d, %d) = %d, want %d", tc.srcW, tc.srcH, tc.maxH, got, tc.want)
			}
		})
	}
}
