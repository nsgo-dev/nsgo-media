package player

import (
	"encoding/binary"
	"math"
	"runtime"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/gpu"

	"github.com/oneo-me/nsgo-media/internal/pipeline"
)

// gpuTarget reports a target whose hardware pictures a view imports on
// the GPU: Windows' D3D11VA and Linux' VAAPI. Others convert them to
// RGBA in memory.
func gpuTarget(env *core.Env) bool {
	return (runtime.GOOS == "windows" || runtime.GOOS == "linux") && env.SupportsGPU()
}

// yuvWGSL converts a picture's luma and chroma planes to RGB with the
// rows of an affine matrix.
const yuvWGSL = `
struct Params {
	r: vec4<f32>,
	g: vec4<f32>,
	b: vec4<f32>,
	pad: vec4<f32>,
}

@group(0) @binding(0) var luma: texture_2d<f32>;
@group(0) @binding(1) var chroma: texture_2d<f32>;
@group(0) @binding(2) var samp: sampler;
@group(0) @binding(3) var<uniform> params: Params;

struct VOut {
	@builtin(position) pos: vec4<f32>,
	@location(0) uv: vec2<f32>,
}

@vertex
fn vs_main(@location(0) pos: vec2<f32>, @location(1) uv: vec2<f32>) -> VOut {
	var out: VOut;
	out.pos = vec4<f32>(pos, 0.0, 1.0);
	out.uv = uv;
	return out;
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
	let c = textureSample(chroma, samp, in.uv).rg;
	let yuv = vec4<f32>(textureSample(luma, samp, in.uv).r, c.x, c.y, 1.0);
	let rgb = vec3<f32>(dot(params.r, yuv), dot(params.g, yuv), dot(params.b, yuv));
	return vec4<f32>(clamp(rgb, vec3<f32>(0.0), vec3<f32>(1.0)), 1.0);
}
`

// gpuPicture draws a player's pictures on the GPU into a gpu.View, each
// imported from the decoder's surface. It runs on the engine's goroutine.
type gpuPicture struct {
	p    *Player
	cx   *core.Scope
	size *viewSize
	dev  *gpu.Device
	kind pipeline.GPUKind

	pipe    *gpu.Pipeline
	layout  *gpu.BindGroupLayout
	sampler *gpu.Sampler
	quads   *gpu.Buffer
	params  *gpu.Buffer
	matrix  [12]float32

	cur      *imported   // the picture showing
	retiring []*imported // pictures shown before, until the GPU is done
	drawnW   int         // the surface size cur was drawn at
	drawnH   int
	cleared  bool // the surface shows no picture
}

// imported is a frame's picture opened on the view's device.
type imported struct {
	frame         *pipeline.Frame
	release       func()
	luma, chroma  *gpu.Texture
	group         *gpu.BindGroup
	fence         *gpu.FrameFence // of the last frame that sampled it
	width, height int
}

// ready sets the view up on dev and reports the kind of picture it
// imports, GPUNone when it imports none.
func (g *gpuPicture) ready(dev *gpu.Device) pipeline.GPUKind {
	g.drop()
	g.dev, g.kind, g.cleared = dev, pipeline.GPUNone, false
	kind := pipeline.GPUNone
	switch {
	case runtime.GOOS == "windows" && dev.SupportsExternalTexture(gpu.ExternalD3D11Shared):
		kind = pipeline.GPUD3D11
	case runtime.GOOS == "linux" && dev.SupportsExternalTexture(gpu.ExternalDMABuf):
		kind = pipeline.GPUDMABuf
	default:
		return pipeline.GPUNone
	}
	mod, err := gpu.CompileShader(gpu.ShaderDesc{Label: "nsgo-media yuv", Source: yuvWGSL})
	if err != nil {
		return pipeline.GPUNone
	}
	if g.layout, err = dev.CreateBindGroupLayout(g.cx, []gpu.BindGroupLayoutEntry{
		{Binding: 0, Visibility: gpu.StageFragment, Type: gpu.BindingTexture},
		{Binding: 1, Visibility: gpu.StageFragment, Type: gpu.BindingTexture},
		{Binding: 2, Visibility: gpu.StageFragment, Type: gpu.BindingSampler},
		{Binding: 3, Visibility: gpu.StageFragment, Type: gpu.BindingUniform},
	}); err != nil {
		return pipeline.GPUNone
	}
	if g.sampler, err = dev.CreateSampler(g.cx, gpu.SamplerDesc{
		MinFilter: gpu.FilterLinear, MagFilter: gpu.FilterLinear,
		AddressU: gpu.AddressClampToEdge, AddressV: gpu.AddressClampToEdge,
	}); err != nil {
		return pipeline.GPUNone
	}
	if g.quads, err = dev.CreateBuffer(g.cx, gpu.BufferDesc{Size: uint64(len(quadData)), Usage: gpu.UsageVertex, Data: quadData}); err != nil {
		return pipeline.GPUNone
	}
	if g.params, err = dev.CreateBuffer(g.cx, gpu.BufferDesc{Size: 64, Usage: gpu.UsageUniform | gpu.UsageCopyDst}); err != nil {
		return pipeline.GPUNone
	}
	g.matrix = [12]float32{}
	if g.pipe, err = dev.CreatePipeline(g.cx, gpu.PipelineDesc{
		Label:    "nsgo-media picture",
		Vertex:   gpu.ShaderStage{Module: mod, EntryPoint: "vs_main"},
		Fragment: gpu.ShaderStage{Module: mod, EntryPoint: "fs_main"},
		Layouts:  []*gpu.BindGroupLayout{g.layout},
		VertexBuffers: []gpu.VertexBufferLayout{{Stride: 16, Attributes: []gpu.VertexAttribute{
			{Location: 0, Offset: 0, Format: gpu.VertexFloat32x2},
			{Location: 1, Offset: 8, Format: gpu.VertexFloat32x2},
		}}},
		Primitive: gpu.PrimitiveState{Topology: gpu.TopologyTriangleList},
		Targets:   []gpu.ColorTargetState{{Format: dev.SurfaceFormat()}},
	}); err != nil {
		return pipeline.GPUNone
	}
	g.kind = kind
	return kind
}

// lost forgets the device, whose resources went with it.
func (g *gpuPicture) lost() {
	g.drop()
	g.dev, g.kind = nil, pipeline.GPUNone
}

// drop lets go of every picture imported.
func (g *gpuPicture) drop() {
	if g.cur != nil {
		g.retiring = append(g.retiring, g.cur)
		g.cur = nil
	}
	for _, im := range g.retiring {
		im.close()
	}
	g.retiring = nil
}

func (im *imported) close() {
	for _, c := range []interface{ Close() error }{im.group, im.luma, im.chroma} {
		if c != nil {
			c.Close()
		}
	}
	im.release()
}

// draw shows f, the frame showing, when it is a picture on the GPU, and
// lets go of the pictures the GPU is done with. A frame in memory or none
// leaves the surface empty, under the image view that shows those.
func (g *gpuPicture) draw(f *pipeline.Frame) {
	kept := g.retiring[:0]
	for _, im := range g.retiring {
		if im.fence.Signaled() {
			im.close()
		} else {
			kept = append(kept, im)
		}
	}
	g.retiring = kept
	dev := g.dev
	if dev == nil || dev.IsLost() || dev.IsSuspended() {
		return
	}
	pic := (*pipeline.GPUPicture)(nil)
	if f != nil && g.kind != pipeline.GPUNone {
		pic = f.GPU
		if pic != nil && pic.Kind != g.kind {
			pic = nil
		}
	}
	if g.cur != nil && (pic == nil || g.cur.frame != f) {
		g.retiring = append(g.retiring, g.cur)
		g.cur = nil
	}
	w, h := dev.SurfaceSize()
	if pic == nil {
		if !g.cleared {
			g.submit(nil, 0)
			g.cleared = true
		}
		return
	}
	if g.cur == nil {
		im := g.open(f)
		if im == nil {
			// Pictures go through memory from here on.
			g.kind, g.size.kind = pipeline.GPUNone, pipeline.GPUNone
			g.p.updateGPU()
			return
		}
		g.cur = im
	} else if g.drawnW == w && g.drawnH == h {
		return // drawn already
	}
	g.setMatrix(pic)
	g.cur.fence = g.submit(g.cur.group, rotationIndex(pic.Rotation))
	g.drawnW, g.drawnH = w, h
	g.cleared = false
}

// open imports f's planes, holding f until the GPU is done with them.
func (g *gpuPicture) open(f *pipeline.Frame) *imported {
	pic := f.GPU
	cw, ch := (pic.Width+1)/2, (pic.Height+1)/2
	lumaFormat, chromaFormat := gpu.FormatR8Unorm, gpu.FormatRG8Unorm
	if pic.Deep {
		lumaFormat, chromaFormat = gpu.FormatR16Unorm, gpu.FormatRG16Unorm
	}
	desc := func(plane int, w, h int, format gpu.TextureFormat) gpu.ExternalTextureDesc {
		d := gpu.ExternalTextureDesc{Width: uint32(w), Height: uint32(h), Format: format}
		switch pic.Kind {
		case pipeline.GPUD3D11:
			d.Kind, d.Handle, d.Plane = gpu.ExternalD3D11Shared, pic.Handle, uint32(plane)
			d.KeyedMutex, d.AcquireKey, d.ReleaseKey = true, 1, 1
		case pipeline.GPUDMABuf:
			pl := pic.Planes[plane]
			d.Kind, d.FourCC, d.Modifier = gpu.ExternalDMABuf, pl.FourCC, pl.Modifier
			d.Planes = []gpu.DMABufPlane{{FD: pl.FD, Offset: pl.Offset, Pitch: pl.Pitch}}
		}
		return d
	}
	im := &imported{frame: f, release: g.p.pl.Hold(f), width: pic.Width, height: pic.Height}
	var err error
	if im.luma, err = g.dev.ImportTexture(g.cx, desc(0, pic.Width, pic.Height, lumaFormat)); err == nil {
		if im.chroma, err = g.dev.ImportTexture(g.cx, desc(1, cw, ch, chromaFormat)); err == nil {
			im.group, err = g.dev.CreateBindGroup(g.cx, g.layout, []gpu.BindGroupEntry{
				{Binding: 0, Texture: im.luma},
				{Binding: 1, Texture: im.chroma},
				{Binding: 2, Sampler: g.sampler},
				{Binding: 3, Buffer: g.params},
			})
		}
	}
	if err != nil {
		im.close()
		return nil
	}
	return im
}

// submit draws a frame: the picture of group turned by quad, or nothing.
func (g *gpuPicture) submit(group *gpu.BindGroup, quad int) *gpu.FrameFence {
	fr := g.dev.BeginFrame(time.Now())
	pass := fr.BeginRenderPass(gpu.RenderPassDesc{ColorAttachments: []gpu.ColorAttachment{{
		LoadOp: gpu.LoadClear, StoreOp: gpu.StoreStore, ClearColor: core.RGBA(0x000000, 1),
	}}})
	if group != nil {
		pass.SetPipeline(g.pipe)
		pass.SetBindGroup(0, group)
		pass.SetVertexBuffer(0, g.quads, 0)
		pass.Draw(6, uint32(quad*6))
	}
	pass.End()
	fr.Submit()
	return fr.Fence()
}

// setMatrix writes the conversion of pic's samples to RGB, when it
// changed.
func (g *gpuPicture) setMatrix(pic *pipeline.GPUPicture) {
	m := yuvMatrix(pic)
	if m == g.matrix {
		return
	}
	g.matrix = m
	buf := make([]byte, 64)
	for i, v := range m {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	g.dev.WriteBuffer(g.params, 0, buf)
}

// yuvMatrix is the affine conversion of a picture's sampled planes,
// normalized as the GPU samples them, to RGB: rows for R, G and B of
// (Y, Cb, Cr, 1).
func yuvMatrix(pic *pipeline.GPUPicture) [12]float32 {
	kr, kb := 0.2126, 0.0722 // BT.709
	switch pic.ColorSpace {
	case 5, 6: // BT.470 BG, SMPTE 170M
		kr, kb = 0.299, 0.114
	case 9, 10: // BT.2020
		kr, kb = 0.2627, 0.0593
	case 2: // unspecified: HD pictures are BT.709 in practice, as the converter takes them
		if pic.Height < 720 {
			kr, kb = 0.299, 0.114
		}
	}
	kg := 1 - kr - kb
	// A sample's code value: 8 bits fill an 8-bit plane, 10 bits the top
	// of a 16-bit one.
	bits, scale := 8.0, 255.0
	if pic.Deep {
		bits, scale = 10, 65535.0/64
	}
	unit := math.Exp2(bits - 8)
	// Y' = ay·y + by, and Cb, Cr = ac·c + bc.
	var ay, by, ac, bc float64
	if pic.FullRange {
		top := math.Exp2(bits) - 1
		ay, by = scale/top, 0
		ac, bc = scale/top, -math.Exp2(bits-1)/top
	} else {
		ay, by = scale/(219*unit), -16/219.0
		ac, bc = scale/(224*unit), -128/224.0
	}
	rv := 2 * (1 - kr)
	bu := 2 * (1 - kb)
	gu, gv := bu*kb/kg, rv*kr/kg
	m := [12]float64{
		ay, 0, rv * ac, by + rv*bc,
		ay, -gu * ac, -gv * ac, by - (gu+gv)*bc,
		ay, bu * ac, 0, by + bu*bc,
	}
	var out [12]float32
	for i, v := range m {
		out[i] = float32(v)
	}
	return out
}

func rotationIndex(deg int) int {
	switch deg {
	case 90:
		return 1
	case 180:
		return 2
	case 270:
		return 3
	}
	return 0
}

// quadData holds a quad covering the surface for each rotation, 0°, 90°,
// 180° and 270° clockwise: six vertices of position and texture
// coordinates each.
var quadData = func() []byte {
	// The surface's corners, top left clockwise, and the picture's
	// corners that show there unturned.
	pos := [4][2]float32{{-1, 1}, {1, 1}, {1, -1}, {-1, -1}}
	uv := [4][2]float32{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	var b []byte
	for r := range 4 {
		corner := func(i int) [4]float32 {
			// Turning clockwise by r quarters shows at corner i the
			// picture's corner r before it.
			t := uv[(i+4-r)%4]
			return [4]float32{pos[i][0], pos[i][1], t[0], t[1]}
		}
		for _, i := range []int{0, 1, 2, 0, 2, 3} {
			for _, v := range corner(i) {
				b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
			}
		}
	}
	return b
}()
