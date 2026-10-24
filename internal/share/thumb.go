package share

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
)

// ThumbSize est la taille (px) des miniatures.
const ThumbSize = 256

// ThumbPixelBudget plafonne le nombre de pixels testés pendant la rasterisation (somme des
// boîtes englobantes des triangles). Un vrai modèle, fait de petits triangles, en consomme une
// fraction ; un fichier piégé (grands triangles superposés) donne une miniature partielle au
// lieu de monopoliser le processeur plusieurs minutes. Modifiable en test.
var ThumbPixelBudget = 200_000_000

// ThumbKey renvoie le nom du fichier miniature associé à un maillage.
func ThumbKey(meshKey string) string { return strings.TrimSuffix(meshKey, ".fmm") + ".png" }

// RenderThumbnail dessine le maillage en vue isométrique (couleur « argile », fond transparent)
// et renvoie un PNG. Rasterisation logicielle avec tampon de profondeur et suréchantillonnage 2×.
func RenderThumbnail(m *Mesh) ([]byte, error) {
	const ss = 2
	size := ThumbSize * ss

	// Vue : rotation de -45° autour de Z, puis caméra inclinée de 30° au-dessus de l'horizon.
	az, el := -math.Pi/4, math.Pi/6
	ca, sa, ce, se := math.Cos(az), math.Sin(az), math.Cos(el), math.Sin(el)
	n := len(m.Positions) / 3
	sx := make([]float64, n)
	sy := make([]float64, n)
	sz := make([]float64, n)
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for i := range n {
		x, y, z := float64(m.Positions[i*3]), float64(m.Positions[i*3+1]), float64(m.Positions[i*3+2])
		x1 := x*ca - y*sa
		y1 := x*sa + y*ca
		sx[i] = x1
		sy[i] = z*ce + y1*se // le haut de l'écran
		sz[i] = y1*ce - z*se // profondeur : plus grand = plus loin
		minX, maxX = math.Min(minX, sx[i]), math.Max(maxX, sx[i])
		minY, maxY = math.Min(minY, sy[i]), math.Max(maxY, sy[i])
	}
	extent := math.Max(maxX-minX, maxY-minY)
	if extent == 0 {
		extent = 1
	}
	scale := float64(size) * 0.86 / extent
	offX := float64(size)/2 - (minX+maxX)/2*scale
	offY := float64(size)/2 + (minY+maxY)/2*scale
	for i := range n {
		sx[i] = sx[i]*scale + offX
		sy[i] = offY - sy[i]*scale
	}

	depth := make([]float64, size*size)
	for i := range depth {
		depth[i] = math.Inf(1)
	}
	shade := make([]float64, size*size) // 0 = vide
	// Lumière venant du haut à gauche, légèrement vers la caméra.
	lx, ly, lz := -0.35, 0.85, -0.4

	budget := ThumbPixelBudget
	for t := 0; t+2 < n && budget > 0; t += 3 {
		a, b, c := t, t+1, t+2
		// Normale dans l'espace écran (x, haut, profondeur) pour l'éclairage.
		ux, uy, uz := sx[b]-sx[a], -(sy[b] - sy[a]), sz[b]-sz[a]
		vx, vy, vz := sx[c]-sx[a], -(sy[c] - sy[a]), sz[c]-sz[a]
		nx, ny, nz := uy*vz-uz*vy, uz*vx-ux*vz, ux*vy-uy*vx
		nl := math.Sqrt(nx*nx + ny*ny + nz*nz)
		if nl == 0 {
			continue
		}
		lum := 0.28 + 0.72*math.Abs((nx*lx+ny*ly+nz*lz)/nl) // les STL n'orientent pas toujours leurs faces

		x0 := int(math.Max(0, math.Floor(min(sx[a], sx[b], sx[c]))))
		x1 := int(math.Min(float64(size-1), math.Ceil(max(sx[a], sx[b], sx[c]))))
		y0 := int(math.Max(0, math.Floor(min(sy[a], sy[b], sy[c]))))
		y1 := int(math.Min(float64(size-1), math.Ceil(max(sy[a], sy[b], sy[c]))))
		area := (sx[b]-sx[a])*(sy[c]-sy[a]) - (sx[c]-sx[a])*(sy[b]-sy[a])
		if area == 0 || x1 < x0 || y1 < y0 {
			continue
		}
		budget -= (x1 - x0 + 1) * (y1 - y0 + 1)
		for py := y0; py <= y1; py++ {
			fy := float64(py) + 0.5
			for px := x0; px <= x1; px++ {
				fx := float64(px) + 0.5
				w0 := ((sx[b]-fx)*(sy[c]-fy) - (sx[c]-fx)*(sy[b]-fy)) / area
				w1 := ((sx[c]-fx)*(sy[a]-fy) - (sx[a]-fx)*(sy[c]-fy)) / area
				w2 := 1 - w0 - w1
				if w0 < 0 || w1 < 0 || w2 < 0 {
					continue
				}
				d := w0*sz[a] + w1*sz[b] + w2*sz[c]
				k := py*size + px
				if d < depth[k] {
					depth[k] = d
					shade[k] = lum
				}
			}
		}
	}

	// Réduction 2× (anticrénelage) en couleur « argile ».
	base := [3]float64{196, 184, 170}
	img := image.NewNRGBA(image.Rect(0, 0, ThumbSize, ThumbSize))
	for y := range ThumbSize {
		for x := range ThumbSize {
			var r, g, bl, cov float64
			for dy := range ss {
				for dx := range ss {
					l := shade[(y*ss+dy)*size+x*ss+dx]
					if l == 0 {
						continue
					}
					r, g, bl, cov = r+base[0]*l, g+base[1]*l, bl+base[2]*l, cov+1
				}
			}
			if cov == 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(r / cov), uint8(g / cov), uint8(bl / cov), uint8(255 * cov / (ss * ss))})
		}
	}
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	return buf.Bytes(), err
}
