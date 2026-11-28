package player

import (
	"encoding/binary"
	"math"
	"runtime"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/gpu"

	"github.com/nsgo-dev/nsgo-media/internal/pipeline"
)

// gpuTarget reports a target whose views draw pictures on the GPU:
// Windows' D3D11VA and Linux' VAAPI pictures imported, and macOS' HDR
// pictures uploaded from memory. Others convert them to RGBA in memory.
func gpuTarget(env *core.Env) bool {
	return (runtime.GOOS == "windows" || runtime.GOOS == "linux" || runtime.GOOS == "darwin") && env.SupportsGPU()
}

// yuvWGSL converts a picture's luma and chroma planes to RGB with the
// rows of an affine matrix. On an SDR surface (params.hdr.z == 0), SDR
// pictures pass through clamped to [0, 1] and HDR pictures (PQ or HLG)
// are tone-mapped through ITU-R BT.2390 with BT.2020-to-BT.709 gamut
// conversion to sRGB. On an extended linear sRGB / scRGB HDR surface
// (params.hdr.z == 1), SDR pictures are linearized to [0, 1] and HDR
// pictures output unclamped linear light (1.0 = 203-nit SDR white) in
// Rec. 709 primaries. Planes uploaded with 16-bit samples
// (params.hdr.w == 1) hold each sample as its low and high bytes, which
// filter linearly as the sample would.
const yuvWGSL = `
struct Params {
	r: vec4<f32>,
	g: vec4<f32>,
	b: vec4<f32>,
	hdr: vec4<f32>,
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

fn pq_eotf_s(v: f32) -> f32 {
	let c = clamp(v, 0.0, 1.0);
	if (c <= 0.0) {
		return 0.0;
	}
	let p = pow(c, 0.012683313515655966);
	let num = max(p - 0.8359375, 0.0);
	let den = max(18.8515625 - 18.6875 * p, 0.000001);
	return pow(num / den, 6.277394636015326);
}

fn pq_oetf_s(l: f32) -> f32 {
	let c = clamp(l, 0.0, 1.0);
	if (c <= 0.0) {
		return 0.0;
	}
	let lm = pow(c, 0.1593017578125);
	return pow((0.8359375 + 18.8515625 * lm) / (1.0 + 18.6875 * lm), 78.84375);
}

fn hlg_inv_oetf_s(v: f32) -> f32 {
	let c = clamp(v, 0.0, 1.0);
	if (c <= 0.5) {
		return c * c / 3.0;
	}
	return (pow(2.718281828459045, (c - 0.55991073) / 0.17883277) + 0.28466892) / 12.0;
}

fn bt2390_linear(l: f32) -> f32 {
	let pq_max = 0.751827096247041;
	let max_lum = 0.7723710726867074;
	let ks = 0.6585566090300611;
	let e1 = min(pq_oetf_s(l) / pq_max, 1.0);
	var e2 = e1;
	if (e1 > ks) {
		let t = (e1 - ks) / (1.0 - ks);
		let t2 = t * t;
		let t3 = t2 * t;
		e2 = (2.0 * t3 - 3.0 * t2 + 1.0) * ks + (t3 - 2.0 * t2 + t) * (1.0 - ks) + (-2.0 * t3 + 3.0 * t2) * max_lum;
	}
	return pq_eotf_s(e2 * pq_max) * 49.26108374384236;
}

fn srgb_oetf_s(c: f32) -> f32 {
	let v = clamp(c, 0.0, 1.0);
	if (v <= 0.0031308) {
		return 12.92 * v;
	}
	return 1.055 * pow(v, 0.4166666666666667) - 0.055;
}

fn srgb_eotf_s(v: f32) -> f32 {
	let c = clamp(v, 0.0, 1.0);
	if (c <= 0.04045) {
		return c / 12.92;
	}
	return pow((c + 0.055) / 1.055, 2.4);
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
	let l = textureSample(luma, samp, in.uv);
	let c = textureSample(chroma, samp, in.uv);
	var yuv = vec4<f32>(l.r, c.r, c.g, 1.0);
	if (params.hdr.w > 0.5) {
		let k = vec2<f32>(255.0, 65280.0) / 65535.0;
		yuv = vec4<f32>(dot(l.rg, k), dot(c.rg, k), dot(c.ba, k), 1.0);
	}
	let rgb = vec3<f32>(dot(params.r, yuv), dot(params.g, yuv), dot(params.b, yuv));
	let c0 = clamp(rgb, vec3<f32>(0.0), vec3<f32>(1.0));
	if (params.hdr.x < 0.5) {
		if (params.hdr.z > 0.5) {
			return vec4<f32>(srgb_eotf_s(c0.x), srgb_eotf_s(c0.y), srgb_eotf_s(c0.z), 1.0);
		}
		return vec4<f32>(c0, 1.0);
	}
	var lin = vec3<f32>(0.0, 0.0, 0.0);
	if (params.hdr.x < 1.5) {
		lin = vec3<f32>(pq_eotf_s(c0.x), pq_eotf_s(c0.y), pq_eotf_s(c0.z));
	} else {
		let e = vec3<f32>(hlg_inv_oetf_s(c0.x), hlg_inv_oetf_s(c0.y), hlg_inv_oetf_s(c0.z));
		let ys = dot(e, vec3<f32>(0.2627, 0.6780, 0.0593));
		if (ys > 0.0) {
			lin = e * (pow(ys, 0.2) * 0.1);
		}
	}
	if (params.hdr.z > 0.5) {
		lin = lin * 49.26108374384236;
	} else {
		let m = max(lin.x, max(lin.y, lin.z));
		if (m > 0.0000001) {
			lin = lin * (bt2390_linear(m) / m);
		}
	}
	if (params.hdr.y > 0.5) {
		lin = vec3<f32>(
			1.6604910 * lin.x - 0.5876411 * lin.y - 0.0728499 * lin.z,
			-0.1245505 * lin.x + 1.1328999 * lin.y - 0.0083494 * lin.z,
			-0.0181508 * lin.x - 0.1005789 * lin.y + 1.1187297 * lin.z,
		);
	}
	if (params.hdr.z > 0.5) {
		return vec4<f32>(lin, 1.0);
	}
	return vec4<f32>(srgb_oetf_s(lin.x), srgb_oetf_s(lin.y), srgb_oetf_s(lin.z), 1.0);
}
`

// gpuPicture draws a player's pictures on the GPU into a gpu.View, each
// imported from the decoder's surface or uploaded from memory. It runs on
// the engine's goroutine.
type gpuPicture struct {
	p         *Player
	cx        *core.Scope
	size      *viewSize
	dev       *gpu.Device
	kind      pipeline.GPUKind
	surfaceCS gpu.ColorSpace

	pipe       *gpu.Pipeline
	layout     *gpu.BindGroupLayout
	sampler    *gpu.Sampler
	quads      *gpu.Buffer
	params     *gpu.Buffer
	paramsData [16]float32
	paramsSet  bool

	up *uploaded // the textures pictures in memory upload into

	cur      *imported   // the picture showing
	retiring []*imported // pictures shown before, until the GPU is done
	drawnW   int         // the surface size cur was drawn at
	drawnH   int
	cleared  bool // the surface shows no picture
}

// imported is a frame's picture opened on the view's device: imported
// planes it owns, or the upload textures it was written into.
type imported struct {
	frame         *pipeline.Frame
	release       func()
	luma, chroma  *gpu.Texture
	group         *gpu.BindGroup
	shared        bool            // luma, chroma and group are the view's upload textures
	fence         *gpu.FrameFence // of the last frame that sampled it
	width, height int
}

// uploaded is the planes' textures pictures in memory are written into,
// kept while their size and depth stay. Writes and draws run in order on
// the GPU's queue, so one set serves every frame.
type uploaded struct {
	luma, chroma *gpu.Texture
	group        *gpu.BindGroup
	w, h         int
	deep         bool
}

func (u *uploaded) close() {
	for _, c := range []interface{ Close() error }{u.group, u.luma, u.chroma} {
		if c != nil {
			c.Close()
		}
	}
}

// ready sets the view up on dev and reports the kind of picture it
// draws, GPUNone when it draws none.
func (g *gpuPicture) ready(dev *gpu.Device) pipeline.GPUKind {
	g.drop()
	g.dev, g.kind, g.surfaceCS, g.cleared = dev, pipeline.GPUNone, dev.SurfaceColorSpace(), false
	kind := pipeline.GPUNone
	switch {
	case runtime.GOOS == "windows" && dev.SupportsExternalTexture(gpu.ExternalD3D11Shared):
		kind = pipeline.GPUD3D11
	case runtime.GOOS == "linux" && dev.SupportsExternalTexture(gpu.ExternalDMABuf):
		kind = pipeline.GPUDMABuf
	case runtime.GOOS == "darwin":
		// Until the view imports VideoToolbox's surfaces (#40).
		kind = pipeline.GPUUpload
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
	g.paramsData, g.paramsSet = [16]float32{}, false
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
	if g.up != nil {
		g.up.close()
		g.up = nil
	}
}

func (im *imported) close() {
	if !im.shared {
		for _, c := range []interface{ Close() error }{im.group, im.luma, im.chroma} {
			if c != nil {
				c.Close()
			}
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

// open imports f's planes, holding f until the GPU is done with them, or
// uploads them from memory.
func (g *gpuPicture) open(f *pipeline.Frame) *imported {
	pic := f.GPU
	if pic.Kind == pipeline.GPUUpload {
		return g.upload(f)
	}
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

// upload writes f's planes in memory into the upload textures, made
// again when its size or depth changed. The writes copy the planes, so f
// need not be held.
func (g *gpuPicture) upload(f *pipeline.Frame) *imported {
	pic := f.GPU
	u := g.up
	if u == nil || u.w != pic.Width || u.h != pic.Height || u.deep != pic.Deep {
		if u != nil {
			u.close()
			g.up = nil
		}
		// 16-bit samples upload as byte pairs: the backends create no
		// 16-bit textures.
		lumaFormat, chromaFormat := gpu.FormatR8Unorm, gpu.FormatRG8Unorm
		if pic.Deep {
			lumaFormat, chromaFormat = gpu.FormatRG8Unorm, gpu.FormatRGBA8Unorm
		}
		u = &uploaded{w: pic.Width, h: pic.Height, deep: pic.Deep}
		tex := func(w, h int, format gpu.TextureFormat) (*gpu.Texture, error) {
			return g.dev.CreateTexture(g.cx, gpu.TextureDesc{
				Label: "nsgo-media plane", Width: uint32(w), Height: uint32(h), Format: format,
				Usage: gpu.TextureSampled | gpu.TextureCopyDst,
			})
		}
		var err error
		if u.luma, err = tex(pic.Width, pic.Height, lumaFormat); err == nil {
			if u.chroma, err = tex((pic.Width+1)/2, (pic.Height+1)/2, chromaFormat); err == nil {
				u.group, err = g.dev.CreateBindGroup(g.cx, g.layout, []gpu.BindGroupEntry{
					{Binding: 0, Texture: u.luma},
					{Binding: 1, Texture: u.chroma},
					{Binding: 2, Sampler: g.sampler},
					{Binding: 3, Buffer: g.params},
				})
			}
		}
		if err != nil {
			u.close()
			return nil
		}
		g.up = u
	}
	for i, t := range []*gpu.Texture{u.luma, u.chroma} {
		pl := pic.Planes[i]
		g.dev.WriteTexture(t, gpu.TextureCopy{Texture: t}, pl.Data, gpu.DataLayout{BytesPerRow: pl.Pitch})
	}
	return &imported{
		frame: f, release: func() {}, luma: u.luma, chroma: u.chroma, group: u.group, shared: true,
		width: pic.Width, height: pic.Height,
	}
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

// setMatrix writes the conversion of pic's samples to RGB and its HDR
// tone-mapping parameters, when they changed.
func (g *gpuPicture) setMatrix(pic *pipeline.GPUPicture) {
	p := shaderParams(pic, g.surfaceCS)
	if g.paramsSet && p == g.paramsData {
		return
	}
	g.paramsData, g.paramsSet = p, true
	buf := make([]byte, 64)
	for i, v := range p {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	g.dev.WriteBuffer(g.params, 0, buf)
}

// shaderParams packs the YUV-to-RGB matrix and HDR mode flags into the
// shader's 16-float uniform block: hdr.x is the input transfer (0 SDR,
// 1 PQ, 2 HLG), hdr.y is 1 for BT.2020-to-BT.709 gamut conversion, and
// hdr.z is 1 when outputting linear extended-range sRGB (scRGB) to an
// HDR surface, and hdr.w is 1 for 16-bit samples uploaded as byte pairs.
func shaderParams(pic *pipeline.GPUPicture, surfaceCS gpu.ColorSpace) [16]float32 {
	m := yuvMatrix(pic)
	var out [16]float32
	copy(out[:12], m[:])
	switch pic.ColorTrc {
	case 16: // AVCOL_TRC_SMPTE2084 (PQ / HDR10)
		out[12] = 1
	case 18: // AVCOL_TRC_ARIB_STD_B67 (HLG)
		out[12] = 2
	}
	if out[12] > 0 && (pic.ColorPrimaries == 9 || pic.ColorPrimaries == 2 || pic.ColorPrimaries == 0) {
		out[13] = 1 // BT.2020 -> BT.709
	}
	if surfaceCS == gpu.ColorSpaceExtendedLinearSRGB {
		out[14] = 1
	}
	if pic.Kind == pipeline.GPUUpload && pic.Deep {
		out[15] = 1
	}
	return out
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
	case 2: // unspecified: HDR pictures are BT.2020; HD SDR are BT.709
		if pic.ColorTrc == 16 || pic.ColorTrc == 18 {
			kr, kb = 0.2627, 0.0593
		} else if pic.Height < 720 {
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
