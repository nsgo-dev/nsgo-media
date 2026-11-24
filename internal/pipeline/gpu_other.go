//go:build !windows && !linux

package pipeline

import "github.com/oneo-me/nsgo-media/internal/ffmpeg"

// gpuState is empty where pictures never go to the GPU.
type gpuState struct{}

func (*gpuState) export(*ffmpeg.Frame, *GPUPicture) error { return errNoGPU }
func (*gpuState) unmap()                                  {}
func (*gpuState) free()                                   {}

// gpuSurfaces are the hardware surfaces frames on the GPU hold.
const gpuSurfaces = 0
