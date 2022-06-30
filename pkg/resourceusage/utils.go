package resourceusage

import (
	"strings"

	"github.com/BTBurke/k8sresource"
	"github.com/c2h5oh/datasize"
)

// allow Mi, Gi to mean Mb, Gb
// remove spaces
// lowercase
func convertBytesString(st string) string {
	st = strings.ToLower(st)
	st = strings.ReplaceAll(st, "i", "b")
	st = strings.ReplaceAll(st, " ", "")
	return st
}

func ConvertResourceUsageConfig(usage ResourceUsageConfig) (ResourceUsageData, error) {
	data := ResourceUsageData{}

	cpu, err := k8sresource.NewCPUFromString(convertBytesString(usage.CPU))
	if err != nil {
		return data, err
	}

	memory, err := datasize.ParseString(convertBytesString(usage.Memory))
	if err != nil {
		return data, err
	}

	disk, err := datasize.ParseString(convertBytesString(usage.Disk))
	if err != nil {
		return data, err
	}

	data.CPU = cpu.ToFloat64()
	data.Memory = memory.Bytes()
	data.Disk = disk.Bytes()

	return data, nil
}
