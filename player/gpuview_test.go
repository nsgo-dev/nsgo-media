package player

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/gpu"

	"github.com/oneo-me/nsgo-media/internal/pipeline"
)

func TestYUVShaderCompiles(t *testing.T) {
	m, err := gpu.CompileShader(gpu.ShaderDesc{Source: yuvWGSL})
	if err != nil {
		t.Fatal(err)
	}
	if !m.HasVertexEntry("vs_main") || !m.HasFragmentEntry("fs_main") {
		t.Fatal("the shader lacks its entry points")
	}
}

// convert applies m to sampled (y, cb, cr) as the shader does.
func convert(m [12]float32, y, cb, cr float64) [3]float64 {
	var rgb [3]float64
	for i := range 3 {
		r := m[i*4 : i*4+4]
		rgb[i] = float64(r[0])*y + float64(r[1])*cb + float64(r[2])*cr + float64(r[3])
	}
	return rgb
}

func near(t *testing.T, what string, got [3]float64, want [3]float64) {
	t.Helper()
	for i := range 3 {
		if math.Abs(got[i]-want[i]) > 0.004 {
			t.Errorf("%s = %.4f, want %.4f", what, got, want)
			return
		}
	}
}

func TestYUVMatrix(t *testing.T) {
	// 8-bit limited range: 16 is black, 235 white, 128 no colour.
	hd := yuvMatrix(&pipeline.GPUPicture{Height: 1080, ColorSpace: 1})
	near(t, "black", convert(hd, 16/255.0, 128/255.0, 128/255.0), [3]float64{0, 0, 0})
	near(t, "white", convert(hd, 235/255.0, 128/255.0, 128/255.0), [3]float64{1, 1, 1})
	// BT.709 red is Y 63, Cb 102, Cr 240.
	near(t, "BT.709 red", convert(hd, 63/255.0, 102/255.0, 240/255.0), [3]float64{1, 0, 0})
	// BT.601 red is Y 81, Cb 90, Cr 240; untagged SD pictures are BT.601.
	sd := yuvMatrix(&pipeline.GPUPicture{Height: 480, ColorSpace: 2})
	near(t, "BT.601 red", convert(sd, 81/255.0, 90/255.0, 240/255.0), [3]float64{1, 0, 0})
	// Full range uses every code value.
	full := yuvMatrix(&pipeline.GPUPicture{Height: 1080, ColorSpace: 1, FullRange: true})
	near(t, "full-range white", convert(full, 1, 128/255.0, 128/255.0), [3]float64{1, 1, 1})
	// P010 holds 10 bits at the top of 16: 64 is black, 940 white.
	deep := yuvMatrix(&pipeline.GPUPicture{Height: 2160, ColorSpace: 9, Deep: true})
	p010 := func(v float64) float64 { return v * 64 / 65535 }
	near(t, "10-bit black", convert(deep, p010(64), p010(512), p010(512)), [3]float64{0, 0, 0})
	near(t, "10-bit white", convert(deep, p010(940), p010(512), p010(512)), [3]float64{1, 1, 1})
}

func TestQuadsTurnClockwise(t *testing.T) {
	// The texture coordinates showing at the surface's top left corner,
	// the first vertex of each rotation's quad.
	want := [4][2]float32{{0, 0}, {0, 1}, {1, 1}, {1, 0}}
	for r, w := range want {
		at := r * 6 * 16
		pos := [2]float32{f32(quadData[at:]), f32(quadData[at+4:])}
		uv := [2]float32{f32(quadData[at+8:]), f32(quadData[at+12:])}
		if pos != [2]float32{-1, 1} || uv != w {
			t.Errorf("rotation %d° shows %v at %v, want %v at the top left", r*90, uv, pos, w)
		}
	}
}

func f32(b []byte) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(b)) }

func TestShaderParams(t *testing.T) {
	sdr := shaderParams(&pipeline.GPUPicture{Height: 1080, ColorSpace: 1, ColorTrc: 1, ColorPrimaries: 1}, gpu.ColorSpaceSRGB)
	if sdr[12] != 0 || sdr[13] != 0 || sdr[14] != 0 {
		t.Fatalf("SDR/SDR hdr flags = (%v, %v, %v), want (0, 0, 0)", sdr[12], sdr[13], sdr[14])
	}
	pq := shaderParams(&pipeline.GPUPicture{Height: 2160, Deep: true, ColorSpace: 9, ColorTrc: 16, ColorPrimaries: 9}, gpu.ColorSpaceSRGB)
	if pq[12] != 1 || pq[13] != 1 || pq[14] != 0 {
		t.Fatalf("PQ/SDR hdr flags = (%v, %v, %v), want (1, 1, 0)", pq[12], pq[13], pq[14])
	}
	hlg := shaderParams(&pipeline.GPUPicture{Height: 2160, Deep: true, ColorSpace: 2, ColorTrc: 18, ColorPrimaries: 2}, gpu.ColorSpaceExtendedLinearSRGB)
	if hlg[12] != 2 || hlg[13] != 1 || hlg[14] != 1 {
		t.Fatalf("HLG/HDR hdr flags = (%v, %v, %v), want (2, 1, 1)", hlg[12], hlg[13], hlg[14])
	}
	if pq[15] != 0 || hlg[15] != 0 {
		t.Fatalf("imported 16-bit planes flagged as byte pairs")
	}
	up := shaderParams(&pipeline.GPUPicture{Kind: pipeline.GPUUpload, Height: 2160, Deep: true, ColorSpace: 9, ColorTrc: 16, ColorPrimaries: 9}, gpu.ColorSpaceExtendedLinearSRGB)
	if up[12] != 1 || up[14] != 1 || up[15] != 1 {
		t.Fatalf("uploaded PQ/HDR flags = (%v, %v, %v), want (1, 1, 1)", up[12], up[14], up[15])
	}
}

// renderYUV runs yuvWGSL on a 4x4 software GPU surface with uniform
// (y, cb, cr) code values (8-bit for NV12, 10-bit shifted left 6 for P010)
// and returns the center pixel's RGBA.
func renderYUV(t *testing.T, pic *pipeline.GPUPicture, yVal, cbVal, crVal uint16) [4]uint8 {
	t.Helper()
	sr := gpu.NewSoftwareRenderer(4, 4, gpu.FormatUndefined)
	dev := sr.Device()
	mod, err := gpu.CompileShader(gpu.ShaderDesc{Label: "test yuv", Source: yuvWGSL})
	if err != nil {
		t.Fatal(err)
	}
	layout, err := dev.CreateBindGroupLayout(nil, []gpu.BindGroupLayoutEntry{
		{Binding: 0, Visibility: gpu.StageFragment, Type: gpu.BindingTexture},
		{Binding: 1, Visibility: gpu.StageFragment, Type: gpu.BindingTexture},
		{Binding: 2, Visibility: gpu.StageFragment, Type: gpu.BindingSampler},
		{Binding: 3, Visibility: gpu.StageFragment, Type: gpu.BindingUniform},
	})
	if err != nil {
		t.Fatal(err)
	}
	sampler, err := dev.CreateSampler(nil, gpu.SamplerDesc{
		MinFilter: gpu.FilterLinear, MagFilter: gpu.FilterLinear,
		AddressU: gpu.AddressClampToEdge, AddressV: gpu.AddressClampToEdge,
	})
	if err != nil {
		t.Fatal(err)
	}
	quads, err := dev.CreateBuffer(nil, gpu.BufferDesc{Size: uint64(len(quadData)), Usage: gpu.UsageVertex, Data: quadData})
	if err != nil {
		t.Fatal(err)
	}
	p := shaderParams(pic, gpu.ColorSpaceSRGB)
	pbuf := make([]byte, 64)
	for i, v := range p {
		binary.LittleEndian.PutUint32(pbuf[i*4:], math.Float32bits(v))
	}
	params, err := dev.CreateBuffer(nil, gpu.BufferDesc{Size: 64, Usage: gpu.UsageUniform | gpu.UsageCopyDst, Data: pbuf})
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := dev.CreatePipeline(nil, gpu.PipelineDesc{
		Vertex:   gpu.ShaderStage{Module: mod, EntryPoint: "vs_main"},
		Fragment: gpu.ShaderStage{Module: mod, EntryPoint: "fs_main"},
		Layouts:  []*gpu.BindGroupLayout{layout},
		VertexBuffers: []gpu.VertexBufferLayout{{Stride: 16, Attributes: []gpu.VertexAttribute{
			{Location: 0, Offset: 0, Format: gpu.VertexFloat32x2},
			{Location: 1, Offset: 8, Format: gpu.VertexFloat32x2},
		}}},
		Primitive: gpu.PrimitiveState{Topology: gpu.TopologyTriangleList},
		Targets:   []gpu.ColorTargetState{{Format: dev.SurfaceFormat()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	lumaFmt, chromaFmt := gpu.FormatR8Unorm, gpu.FormatRG8Unorm
	var lumaBytes, chromaBytes []byte
	var lumaBPR, chromaBPR uint32
	if pic.Deep {
		lumaFmt, chromaFmt = gpu.FormatR16Unorm, gpu.FormatRG16Unorm
		if pic.Kind == pipeline.GPUUpload {
			// The same bytes, uploaded as byte pairs.
			lumaFmt, chromaFmt = gpu.FormatRG8Unorm, gpu.FormatRGBA8Unorm
		}
		lumaBPR, chromaBPR = 4*2, 2*4
		lumaBytes = make([]byte, 4*4*2)
		for i := 0; i < len(lumaBytes); i += 2 {
			binary.LittleEndian.PutUint16(lumaBytes[i:], yVal<<6)
		}
		chromaBytes = make([]byte, 2*2*4)
		for i := 0; i < len(chromaBytes); i += 4 {
			binary.LittleEndian.PutUint16(chromaBytes[i:], cbVal<<6)
			binary.LittleEndian.PutUint16(chromaBytes[i+2:], crVal<<6)
		}
	} else {
		lumaBPR, chromaBPR = 4, 2*2
		lumaBytes = make([]byte, 4*4)
		for i := range lumaBytes {
			lumaBytes[i] = byte(yVal)
		}
		chromaBytes = make([]byte, 2*2*2)
		for i := 0; i < len(chromaBytes); i += 2 {
			chromaBytes[i] = byte(cbVal)
			chromaBytes[i+1] = byte(crVal)
		}
	}
	lumaTex, err := dev.CreateTexture(nil, gpu.TextureDesc{
		Width: 4, Height: 4, Format: lumaFmt, Usage: gpu.TextureSampled | gpu.TextureCopyDst,
	})
	if err != nil {
		t.Fatal(err)
	}
	chromaTex, err := dev.CreateTexture(nil, gpu.TextureDesc{
		Width: 2, Height: 2, Format: chromaFmt, Usage: gpu.TextureSampled | gpu.TextureCopyDst,
	})
	if err != nil {
		t.Fatal(err)
	}
	dev.WriteTexture(lumaTex, gpu.TextureCopy{Texture: lumaTex}, lumaBytes, gpu.DataLayout{BytesPerRow: lumaBPR})
	dev.WriteTexture(chromaTex, gpu.TextureCopy{Texture: chromaTex}, chromaBytes, gpu.DataLayout{BytesPerRow: chromaBPR})
	group, err := dev.CreateBindGroup(nil, layout, []gpu.BindGroupEntry{
		{Binding: 0, Texture: lumaTex},
		{Binding: 1, Texture: chromaTex},
		{Binding: 2, Sampler: sampler},
		{Binding: 3, Buffer: params},
	})
	if err != nil {
		t.Fatal(err)
	}
	fr := dev.BeginFrame(time.Now())
	pass := fr.BeginRenderPass(gpu.RenderPassDesc{ColorAttachments: []gpu.ColorAttachment{{
		LoadOp: gpu.LoadClear, StoreOp: gpu.StoreStore, ClearColor: core.RGBA(0x000000, 1),
	}}})
	pass.SetPipeline(pipe)
	pass.SetBindGroup(0, group)
	pass.SetVertexBuffer(0, quads, 0)
	pass.Draw(6, 0)
	pass.End()
	fr.Submit()

	var px [4]uint8
	var readErr error
	dev.ReadSurface(func(rgba []byte, err error) {
		readErr = err
		if err == nil && len(rgba) >= (2*4+2)*4+4 {
			o := (2*4 + 2) * 4
			copy(px[:], rgba[o:o+4])
		}
	})
	if readErr != nil {
		t.Fatal(readErr)
	}
	return px
}

func TestYUVShaderToneMapping(t *testing.T) {
	// SDR BT.709 red stays unchanged.
	sdr := &pipeline.GPUPicture{Height: 1080, ColorSpace: 1, ColorTrc: 1, ColorPrimaries: 1}
	got := renderYUV(t, sdr, 63, 102, 240)
	if got[0] < 245 || got[1] > 10 || got[2] > 10 || got[3] != 255 {
		t.Errorf("SDR red = %v, want ~(255, 0, 0, 255)", got)
	}

	// HDR10 (PQ, BT.2020): 203-nit white (8-bit limited Y=143) maps bright (>= 210),
	// 1000-nit peak (Y=181) maps >= 245, black (Y=16) stays <= 5, red stays saturated.
	pq := &pipeline.GPUPicture{Height: 2160, ColorSpace: 9, ColorTrc: 16, ColorPrimaries: 9}
	pqWhite := renderYUV(t, pq, 143, 128, 128)
	if pqWhite[0] < 210 || pqWhite[1] < 210 || pqWhite[2] < 210 {
		t.Errorf("PQ 203-nit white = %v, want >= 210", pqWhite)
	}
	pqPeak := renderYUV(t, pq, 181, 128, 128)
	if pqPeak[0] < 245 || pqPeak[0] <= pqWhite[0] {
		t.Errorf("PQ 1000-nit peak = %v, want >= 245 and > ref white (%d)", pqPeak, pqWhite[0])
	}
	pqBlack := renderYUV(t, pq, 16, 128, 128)
	if pqBlack[0] > 5 || pqBlack[1] > 5 || pqBlack[2] > 5 {
		t.Errorf("PQ black = %v, want <= 5", pqBlack)
	}
	pqRed := renderYUV(t, pq, 50, 110, 193)
	if pqRed[0] < 200 || pqRed[1] > 20 || pqRed[2] > 20 {
		t.Errorf("PQ BT.2020 red = %v, want R>=200, G<=20, B<=20", pqRed)
	}

	// HLG (ARIB STD-B67, BT.2020): 75% white (8-bit limited Y=180) maps bright (>= 210),
	// 100% peak (Y=235) maps >= 245, black (Y=16) stays <= 5, red stays saturated.
	hlg := &pipeline.GPUPicture{Height: 2160, ColorSpace: 9, ColorTrc: 18, ColorPrimaries: 9}
	hlgWhite := renderYUV(t, hlg, 180, 128, 128)
	if hlgWhite[0] < 210 || hlgWhite[1] < 210 || hlgWhite[2] < 210 {
		t.Errorf("HLG 75%% white = %v, want >= 210", hlgWhite)
	}
	hlgPeak := renderYUV(t, hlg, 235, 128, 128)
	if hlgPeak[0] < 245 || hlgPeak[0] <= hlgWhite[0] {
		t.Errorf("HLG 100%% peak = %v, want >= 245 and > ref white (%d)", hlgPeak, hlgWhite[0])
	}
	hlgBlack := renderYUV(t, hlg, 16, 128, 128)
	if hlgBlack[0] > 5 || hlgBlack[1] > 5 || hlgBlack[2] > 5 {
		t.Errorf("HLG black = %v, want <= 5", hlgBlack)
	}
	hlgRed := renderYUV(t, hlg, 59, 105, 212)
	if hlgRed[0] < 200 || hlgRed[1] > 20 || hlgRed[2] > 20 {
		t.Errorf("HLG BT.2020 red = %v, want R>=200, G<=20, B<=20", hlgRed)
	}
}

func TestYUVShaderUploadedDeepPlanes(t *testing.T) {
	// 10-bit samples uploaded as byte pairs convert as the same samples
	// at 8 bits do: limited-range white, black and a BT.2020 red, SDR and
	// PQ.
	for _, c := range []struct{ y, cb, cr uint16 }{{940, 512, 512}, {64, 512, 512}, {252, 424, 752}} {
		for _, trc := range []int32{1, 16} {
			nv12 := &pipeline.GPUPicture{Height: 2160, ColorSpace: 9, ColorTrc: trc, ColorPrimaries: 9}
			p010 := *nv12
			p010.Kind, p010.Deep = pipeline.GPUUpload, true
			want := renderYUV(t, nv12, c.y/4, c.cb/4, c.cr/4)
			got := renderYUV(t, &p010, c.y, c.cb, c.cr)
			for i := range got {
				if d := int(got[i]) - int(want[i]); d < -2 || d > 2 {
					t.Errorf("YCbCr %v, transfer %d uploaded = %v, want %v", c, trc, got, want)
					break
				}
			}
		}
	}
}

// renderYUVHDR renders yuvWGSL into an RGBA16Float offscreen target with
// ColorSpaceExtendedLinearSRGB and returns the center pixel's float32 RGB.
func renderYUVHDR(t *testing.T, pic *pipeline.GPUPicture, yVal, cbVal, crVal byte) [3]float32 {
	t.Helper()
	sr := gpu.NewSoftwareRenderer(4, 4, gpu.FormatUndefined)
	dev := sr.Device()
	mod, err := gpu.CompileShader(gpu.ShaderDesc{Label: "test yuv hdr", Source: yuvWGSL})
	if err != nil {
		t.Fatal(err)
	}
	layout, err := dev.CreateBindGroupLayout(nil, []gpu.BindGroupLayoutEntry{
		{Binding: 0, Visibility: gpu.StageFragment, Type: gpu.BindingTexture},
		{Binding: 1, Visibility: gpu.StageFragment, Type: gpu.BindingTexture},
		{Binding: 2, Visibility: gpu.StageFragment, Type: gpu.BindingSampler},
		{Binding: 3, Visibility: gpu.StageFragment, Type: gpu.BindingUniform},
	})
	if err != nil {
		t.Fatal(err)
	}
	sampler, err := dev.CreateSampler(nil, gpu.SamplerDesc{
		MinFilter: gpu.FilterLinear, MagFilter: gpu.FilterLinear,
		AddressU: gpu.AddressClampToEdge, AddressV: gpu.AddressClampToEdge,
	})
	if err != nil {
		t.Fatal(err)
	}
	quads, err := dev.CreateBuffer(nil, gpu.BufferDesc{Size: uint64(len(quadData)), Usage: gpu.UsageVertex, Data: quadData})
	if err != nil {
		t.Fatal(err)
	}
	p := shaderParams(pic, gpu.ColorSpaceExtendedLinearSRGB)
	pbuf := make([]byte, 64)
	for i, v := range p {
		binary.LittleEndian.PutUint32(pbuf[i*4:], math.Float32bits(v))
	}
	params, err := dev.CreateBuffer(nil, gpu.BufferDesc{Size: 64, Usage: gpu.UsageUniform | gpu.UsageCopyDst, Data: pbuf})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := dev.CreateRenderTarget(nil, gpu.RenderTargetDesc{
		Width: 4, Height: 4, ColorFormat: gpu.FormatRGBA16Float,
	})
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := dev.CreatePipeline(nil, gpu.PipelineDesc{
		Vertex:   gpu.ShaderStage{Module: mod, EntryPoint: "vs_main"},
		Fragment: gpu.ShaderStage{Module: mod, EntryPoint: "fs_main"},
		Layouts:  []*gpu.BindGroupLayout{layout},
		VertexBuffers: []gpu.VertexBufferLayout{{Stride: 16, Attributes: []gpu.VertexAttribute{
			{Location: 0, Offset: 0, Format: gpu.VertexFloat32x2},
			{Location: 1, Offset: 8, Format: gpu.VertexFloat32x2},
		}}},
		Primitive: gpu.PrimitiveState{Topology: gpu.TopologyTriangleList},
		Targets:   []gpu.ColorTargetState{{Format: gpu.FormatRGBA16Float}},
	})
	if err != nil {
		t.Fatal(err)
	}
	lumaBytes := make([]byte, 4*4)
	for i := range lumaBytes {
		lumaBytes[i] = yVal
	}
	chromaBytes := make([]byte, 2*2*2)
	for i := 0; i < len(chromaBytes); i += 2 {
		chromaBytes[i], chromaBytes[i+1] = cbVal, crVal
	}
	lumaTex, err := dev.CreateTexture(nil, gpu.TextureDesc{
		Width: 4, Height: 4, Format: gpu.FormatR8Unorm, Usage: gpu.TextureSampled | gpu.TextureCopyDst,
	})
	if err != nil {
		t.Fatal(err)
	}
	chromaTex, err := dev.CreateTexture(nil, gpu.TextureDesc{
		Width: 2, Height: 2, Format: gpu.FormatRG8Unorm, Usage: gpu.TextureSampled | gpu.TextureCopyDst,
	})
	if err != nil {
		t.Fatal(err)
	}
	dev.WriteTexture(lumaTex, gpu.TextureCopy{Texture: lumaTex}, lumaBytes, gpu.DataLayout{BytesPerRow: 4})
	dev.WriteTexture(chromaTex, gpu.TextureCopy{Texture: chromaTex}, chromaBytes, gpu.DataLayout{BytesPerRow: 4})
	group, err := dev.CreateBindGroup(nil, layout, []gpu.BindGroupEntry{
		{Binding: 0, Texture: lumaTex},
		{Binding: 1, Texture: chromaTex},
		{Binding: 2, Sampler: sampler},
		{Binding: 3, Buffer: params},
	})
	if err != nil {
		t.Fatal(err)
	}
	fr := dev.BeginFrame(time.Now())
	pass := fr.BeginRenderPass(gpu.RenderPassDesc{ColorAttachments: []gpu.ColorAttachment{{
		Texture: rt.Color, LoadOp: gpu.LoadClear, StoreOp: gpu.StoreStore, ClearColor: core.RGBA(0x000000, 1),
	}}})
	pass.SetPipeline(pipe)
	pass.SetBindGroup(0, group)
	pass.SetVertexBuffer(0, quads, 0)
	pass.Draw(6, 0)
	pass.End()
	fr.Submit()

	var out [3]float32
	var readErr error
	dev.ReadTexture(rt.Color, gpu.TextureRegion{X: 2, Y: 2, Width: 1, Height: 1}, func(data []byte, err error) {
		readErr = err
		if err == nil && len(data) >= 6 {
			out[0] = f16(binary.LittleEndian.Uint16(data[0:2]))
			out[1] = f16(binary.LittleEndian.Uint16(data[2:4]))
			out[2] = f16(binary.LittleEndian.Uint16(data[4:6]))
		}
	})
	if readErr != nil {
		t.Fatal(readErr)
	}
	return out
}

func f16(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	frac := uint32(h & 0x3ff)
	switch exp {
	case 0:
		if frac == 0 {
			return math.Float32frombits(sign)
		}
		return float32(math.Ldexp(float64(frac), -24)) * func() float32 {
			if sign != 0 {
				return -1
			}
			return 1
		}()
	case 31:
		return math.Float32frombits(sign | 0x7f800000 | (frac << 13))
	default:
		return math.Float32frombits(sign | ((exp + 112) << 23) | (frac << 13))
	}
}

func TestYUVShaderNativeHDROutput(t *testing.T) {
	// SDR mid-gray (sRGB ~0.5 -> Y=126) is linearized to ~0.214 on an scRGB surface.
	sdr := &pipeline.GPUPicture{Height: 1080, ColorSpace: 1, ColorTrc: 1, ColorPrimaries: 1}
	sdrMid := renderYUVHDR(t, sdr, 126, 128, 128)
	if sdrMid[0] < 0.18 || sdrMid[0] > 0.25 {
		t.Errorf("SDR mid-gray on HDR scRGB = %v, want ~0.214", sdrMid)
	}

	// PQ 203-nit white (Y=143) -> ~1.0; 1000-nit peak (Y=181) -> ~4.9 (> 1.0, unclipped HDR!).
	pq := &pipeline.GPUPicture{Height: 2160, ColorSpace: 9, ColorTrc: 16, ColorPrimaries: 9}
	pqWhite := renderYUVHDR(t, pq, 143, 128, 128)
	if pqWhite[0] < 0.85 || pqWhite[0] > 1.15 {
		t.Errorf("PQ 203-nit white on HDR scRGB = %v, want ~1.0", pqWhite)
	}
	pqPeak := renderYUVHDR(t, pq, 181, 128, 128)
	if pqPeak[0] < 4.0 || pqPeak[0] > 6.0 {
		t.Errorf("PQ 1000-nit peak on HDR scRGB = %v, want ~4.9", pqPeak)
	}

	// HLG 75% white (Y=180) -> ~1.0; 100% peak (Y=235) -> ~4.9 (> 1.0, unclipped HDR!).
	hlg := &pipeline.GPUPicture{Height: 2160, ColorSpace: 9, ColorTrc: 18, ColorPrimaries: 9}
	hlgWhite := renderYUVHDR(t, hlg, 180, 128, 128)
	if hlgWhite[0] < 0.85 || hlgWhite[0] > 1.15 {
		t.Errorf("HLG 75%% white on HDR scRGB = %v, want ~1.0", hlgWhite)
	}
	hlgPeak := renderYUVHDR(t, hlg, 235, 128, 128)
	if hlgPeak[0] < 4.0 || hlgPeak[0] > 6.0 {
		t.Errorf("HLG 100%% peak on HDR scRGB = %v, want ~4.9", hlgPeak)
	}
}
