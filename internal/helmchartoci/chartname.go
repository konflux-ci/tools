package helmchartoci

import (
	"fmt"
	"strings"
)

// ChartNameFromImage returns the chart name derived from a full image reference,
// matching the bash task: chart_name="${REPO##*/}" where REPO="${IMAGE%:*}".
// When the reference includes a registry port (no slash before the port colon),
// the full reference is treated as the repository (no tag).
func ChartNameFromImage(image string) (string, error) {
	repo := imageRepo(image)
	if repo == "" {
		return "", fmt.Errorf("image %q: %w", image, errEmptyImage)
	}
	name := repo[strings.LastIndex(repo, "/")+1:]
	if name == "" {
		return "", fmt.Errorf("image %q: %w", image, errInvalidImage)
	}
	return name, nil
}

func imageRepo(image string) string {
	lastColon := strings.LastIndex(image, ":")
	if lastColon < 0 {
		return image
	}
	if strings.Contains(image[:lastColon], "/") {
		return image[:lastColon]
	}
	return image
}

type imageParts struct {
	repo string
	tag  string
}

func splitImageRef(image string) imageParts {
	repo := imageRepo(image)
	tag := "latest"
	if repo != image {
		tag = image[len(repo)+1:]
	}
	return imageParts{repo: repo, tag: tag}
}
