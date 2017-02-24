package google

import (
	"fmt"
	"regexp"
	"strings"

	"google.golang.org/api/googleapi"
)

const (
	resolveImageProjectRegex = "[a-z](?:[-a-z0-9]{0,61}[a-z0-9])?" // TODO(paddy): this isn't based on any documentation; we're just copying the image name restrictions. Need to follow up with @danawillow and/or @evandbrown and see if there's an actual limit to this
	resolveImageFamilyRegex  = "[a-z](?:[-a-z0-9]{0,61}[a-z0-9])?" // TODO(paddy): this isn't based on any documentation; we're just copying the image name restrictions. Need to follow up with @danawillow and/or @evandbrown and see if there's an actual limit to this
	resolveImageImageRegex   = "[a-z](?:[-a-z0-9]{0,61}[a-z0-9])?" // 1-63 characters, lowercase letters, numbers, and hyphens only, beginning and ending in a lowercase letter or number
)

var (
	resolveImageProjectImage           = regexp.MustCompile(fmt.Sprintf("^projects/(%s)/global/images/(%s)$", resolveImageProjectRegex, resolveImageImageRegex))
	resolveImageProjectFamily          = regexp.MustCompile(fmt.Sprintf("^projects/(%s)/global/images/family/(%s)$", resolveImageProjectRegex, resolveImageFamilyRegex))
	resolveImageGlobalImage            = regexp.MustCompile(fmt.Sprintf("^global/images/(%s)$", resolveImageImageRegex))
	resolveImageGlobalFamily           = regexp.MustCompile(fmt.Sprintf("^global/images/family/(%s)$", resolveImageFamilyRegex))
	resolveImageFamilyFamily           = regexp.MustCompile(fmt.Sprintf("^family/(%s)$", resolveImageFamilyRegex))
	resolveImageProjectImageShorthand  = regexp.MustCompile(fmt.Sprintf("^(%s)/(%s)$", resolveImageProjectRegex, resolveImageImageRegex))
	resolveImageProjectFamilyShorthand = regexp.MustCompile(fmt.Sprintf("^(%s)/(%s)$", resolveImageProjectRegex, resolveImageFamilyRegex))
	resolveImageFamily                 = regexp.MustCompile(fmt.Sprintf("^(%s)$", resolveImageFamilyRegex))
	resolveImageImage                  = regexp.MustCompile(fmt.Sprintf("^(%s)$", resolveImageImageRegex))
	resolveImageLink                   = regexp.MustCompile(fmt.Sprintf("^https://www.googleapis.com/compute/v1/projects/(%s)/global/images/(%s)", resolveImageProjectRegex, resolveImageImageRegex))
)

func resolveImageImageExists(c *Config, project, name string) (bool, error) {
	if _, err := c.clientCompute.Images.Get(project, name).Do(); err == nil {
		return true, nil
	} else if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 404 {
		return false, nil
	} else {
		return false, fmt.Errorf("Error checking if image %s exists: %s", name, err)
	}
}

func resolveImageFamilyExists(c *Config, project, name string) (bool, error) {
	if _, err := c.clientCompute.Images.GetFromFamily(project, name).Do(); err == nil {
		return true, nil
	} else if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 404 {
		return false, nil
	} else {
		return false, fmt.Errorf("Error checking if family %s exists: %s", name, err)
	}
}

// If the given name is a URL, return it.
// If it's in the form projects/{project}/global/images/{image}, return it
// If it's in the form projects/{project}/global/images/family/{family}, return it
// If it's in the form global/images/{image}, return it
// If it's in the form global/images/family/{family}, return it
// If it's in the form family/{family}, check if it's a family in the current project. If it is, return it as global/images/family/{family}.
//    If not, check if it could be a GCP-provided family, and if it exists. If it does, return it as projects/{project}/global/images/family/{family}.
// If it's in the form {project}/{family-or-image}, check if it's an image in the named project. If it is, return it as projects/{project}/global/images/{image}.
//    If not, check if it's a family in the named project. If it is, return it as projects/{project}/global/images/family/{family}.
// If it's in the form {family-or-image}, check if it's an image in the current project. If it is, return it as global/images/{image}.
//    If not, check if it could be a GCP-provided image, and if it exists. If it does, return it as projects/{project}/global/images/{image}.
//    If not, check if it's a family in the current project. If it is, return it as global/images/family/{family}.
//    If not, check if it could be a GCP-provided family, and if it exists. If it does, return it as projects/{project}/global/images/family/{family}
func resolveImage(c *Config, name string) (string, error) {
	// built-in projects to look for images/families containing the string
	// on the left in
	imageMap := map[string]string{
		"centos":   "centos-cloud",
		"coreos":   "coreos-cloud",
		"debian":   "debian-cloud",
		"opensuse": "opensuse-cloud",
		"rhel":     "rhel-cloud",
		"sles":     "suse-cloud",
		"ubuntu":   "ubuntu-os-cloud",
		"windows":  "windows-cloud",
	}
	var builtInProject string
	for k, v := range imageMap {
		if strings.Contains(name, k) {
			builtInProject = v
			break
		}
	}
	switch {
	case resolveImageLink.MatchString(name): // https://www.googleapis.com/compute/v1/projects/xyz/global/images/xyz
		return name, nil
	case resolveImageProjectImage.MatchString(name): // projects/xyz/global/images/xyz
		res := resolveImageProjectImage.FindStringSubmatch(name)
		if len(res)-1 != 2 { // subtract one, index zero is the entire matched expression
			return "", fmt.Errorf("Expected %d project image regex matches, got %d for %s", 2, len(res)-1, name)
		}
		return fmt.Sprintf("projects/%s/global/images/%s", res[1], res[2]), nil
	case resolveImageProjectFamily.MatchString(name): // projects/xyz/global/images/family/xyz
		res := resolveImageProjectFamily.FindStringSubmatch(name)
		if len(res)-1 != 2 { // subtract one, index zero is the entire matched expression
			return "", fmt.Errorf("Expected %d project family regex matches, got %d for %s", 2, len(res)-1, name)
		}
		return fmt.Sprintf("projects/%s/global/images/family/%s", res[1], res[2]), nil
	case resolveImageGlobalImage.MatchString(name): // global/images/xyz
		res := resolveImageGlobalImage.FindStringSubmatch(name)
		if len(res)-1 != 1 { // subtract one, index zero is the entire matched expression
			return "", fmt.Errorf("Expected %d global image regex matches, got %d for %s", 1, len(res)-1, name)
		}
		return fmt.Sprintf("global/images/%s", res[1]), nil
	case resolveImageGlobalFamily.MatchString(name): // global/images/family/xyz
		res := resolveImageGlobalFamily.FindStringSubmatch(name)
		if len(res)-1 != 1 { // subtract one, index zero is the entire matched expression
			return "", fmt.Errorf("Expected %d global family regex matches, got %d for %s", 1, len(res)-1, name)
		}
		return fmt.Sprintf("global/images/family/%s", res[1]), nil
	case resolveImageFamilyFamily.MatchString(name): // family/xyz
		res := resolveImageFamilyFamily.FindStringSubmatch(name)
		if len(res)-1 != 1 { // subtract one, index zero is the entire matched expression
			return "", fmt.Errorf("Expected %d family family regex matches, got %d for %s", 1, len(res)-1, name)
		}
		if ok, err := resolveImageFamilyExists(c, c.Project, res[1]); err != nil {
			return "", err
		} else if ok {
			return fmt.Sprintf("global/images/family/%s", res[1]), nil
		}
		if builtInProject != "" {
			if ok, err := resolveImageFamilyExists(c, builtInProject, res[1]); err != nil {
				return "", err
			} else if ok {
				return fmt.Sprintf("projects/%s/global/images/family/%s", builtInProject, res[1]), nil
			}
		}
	case resolveImageProjectImageShorthand.MatchString(name): // xyz/xyz
		res := resolveImageProjectImageShorthand.FindStringSubmatch(name)
		if len(res)-1 != 2 { // subtract one, index zero is the entire matched expression
			return "", fmt.Errorf("Expected %d project image shorthand regex matches, got %d for %s", 2, len(res)-1, name)
		}
		if ok, err := resolveImageImageExists(c, res[1], res[2]); err != nil {
			return "", err
		} else if ok {
			return fmt.Sprintf("projects/%s/global/images/%s", res[1], res[2]), nil
		}
		fallthrough // check if it's a family
	case resolveImageProjectFamilyShorthand.MatchString(name): // xyz/xyz
		res := resolveImageProjectFamilyShorthand.FindStringSubmatch(name)
		if len(res)-1 != 2 { // subtract one, index zero is the entire matched expression
			return "", fmt.Errorf("Expected %d project family shorthand regex matches, got %d for %s", 2, len(res)-1, name)
		}
		if ok, err := resolveImageFamilyExists(c, res[1], res[2]); err != nil {
			return "", err
		} else if ok {
			return fmt.Sprintf("projects/%s/global/images/family/%s", res[1], res[2]), nil
		}
	case resolveImageImage.MatchString(name): // xyz
		res := resolveImageImage.FindStringSubmatch(name)
		if len(res)-1 != 1 { // subtract one, index zero is the entire matched expression
			return "", fmt.Errorf("Expected %d image regex matches, got %d for %s", 1, len(res)-1, name)
		}
		if ok, err := resolveImageImageExists(c, c.Project, res[1]); err != nil {
			return "", err
		} else if ok {
			return fmt.Sprintf("global/images/%s", res[1]), nil
		}
		if builtInProject != "" {
			// check the images GCP provides
			if ok, err := resolveImageImageExists(c, builtInProject, res[1]); err != nil {
				return "", err
			} else if ok {
				return fmt.Sprintf("projects/%s/global/images/%s", builtInProject, res[1]), nil
			}
		}
		fallthrough // check if the name is a family, instead of an image
	case resolveImageFamily.MatchString(name): // xyz
		res := resolveImageFamily.FindStringSubmatch(name)
		if len(res)-1 != 1 { // subtract one, index zero is the entire matched expression
			return "", fmt.Errorf("Expected %d family regex matches, got %d for %s", 1, len(res)-1, name)
		}
		if ok, err := resolveImageFamilyExists(c, c.Project, res[1]); err != nil {
			return "", err
		} else if ok {
			return fmt.Sprintf("global/images/family/%s", res[1]), nil
		}
		if builtInProject != "" {
			// check the families GCP provides
			if ok, err := resolveImageFamilyExists(c, builtInProject, res[1]); err != nil {
				return "", err
			} else if ok {
				return fmt.Sprintf("projects/%s/global/images/family/%s", builtInProject, res[1]), nil
			}
		}
	}
	return "", fmt.Errorf("Could not find image or family %s", name)
}
