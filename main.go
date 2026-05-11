package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

var (
	emptyImage    = ebiten.NewImage(3, 3)
	emptySubImage *ebiten.Image
)

func init() {
	emptyImage.Fill(color.White)
	emptySubImage = emptyImage.SubImage(image.Rect(1, 1, 2, 2)).(*ebiten.Image)
}

const (
	screenW = 900
	screenH = 700
)

type Config struct {
	CellSize    float64
	WallH       float64
	PacRadius   float64
	Gravity     float64
	Friction    float64
	MaxSpeed    float64
	MaxTilt     float64
	TiltSpeed   float64
	TiltDamp    float64
	CamH        float64
	CamD        float64
	CamFovD     float64
	CamYawSpeed float64
	GhostSpeed  float64
	GhostTimer  int
}

var cfg Config

func loadConfig(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("config.json не найден: %v", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("ошибка чтения config.json: %v", err)
	}
}

var (
	mazeLayout []string
	mazeW      int
	mazeH      int
)

func loadMaze(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("maze.txt не найден: %v", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	mazeLayout = nil
	for _, l := range lines {
		if len(l) > 0 {
			mazeLayout = append(mazeLayout, l)
		}
	}
	mazeH = len(mazeLayout)
	mazeW = 0
	for _, l := range mazeLayout {
		if len(l) > mazeW {
			mazeW = len(l)
		}
	}
}

func boardCX() float64 { return float64(mazeW) * cfg.CellSize / 2 }
func boardCZ() float64 { return float64(mazeH) * cfg.CellSize / 2 }

func project(wx, wy, wz, tiltX, tiltZ, yaw float64) (float32, float32) {
	cx, cz := boardCX(), boardCZ()

	rx := wx - cx
	ry := wy
	rz := wz - cz

	cosX, sinX := math.Cos(tiltX), math.Sin(tiltX)
	ry1 := ry*cosX - rz*sinX
	rz1 := ry*sinX + rz*cosX

	cosZ, sinZ := math.Cos(tiltZ), math.Sin(tiltZ)
	rx1 := rx*cosZ - ry1*sinZ
	ry2 := rx*sinZ + ry1*cosZ
	rz2 := rz1

	wx2 := rx1 + cx
	wy2 := ry2
	wz2 := rz2 + cz

	eyeX := cx + math.Sin(yaw)*cfg.CamD
	eyeY := cfg.CamH
	eyeZ := cz + math.Cos(yaw)*cfg.CamD

	vx := wx2 - eyeX
	vy := wy2 - eyeY
	vz := wz2 - eyeZ

	fLen := math.Sqrt(cfg.CamH*cfg.CamH + cfg.CamD*cfg.CamD)
	fwdX := -math.Sin(yaw) * cfg.CamD / fLen
	fwdY := -cfg.CamH / fLen
	fwdZ := -math.Cos(yaw) * cfg.CamD / fLen
	rightX := math.Cos(yaw)
	rightZ := -math.Sin(yaw)
	upX := -math.Sin(yaw) * cfg.CamH / fLen
	upY := cfg.CamD / fLen
	upZ := -math.Cos(yaw) * cfg.CamH / fLen

	csx := vx*rightX + vz*rightZ
	csy := vx*upX + vy*upY + vz*upZ
	csz := vx*fwdX + vy*fwdY + vz*fwdZ

	if csz < 1 {
		csz = 1
	}
	scale := cfg.CamFovD / csz
	return float32(csx*scale + screenW/2), float32(-csy*scale + screenH/2 + 20)
}

func cellDepth(x0, x1, z0, z1, yaw float64) float64 {
	midX := (x0+x1)/2 - boardCX()
	midZ := (z0+z1)/2 - boardCZ()
	return midX*math.Sin(yaw) + midZ*math.Cos(yaw)
}

func drawTri(screen *ebiten.Image, x1, y1, x2, y2, x3, y3 float32, c color.RGBA) {
	cr := float32(c.R) / 255
	cg := float32(c.G) / 255
	cb := float32(c.B) / 255
	ca := float32(c.A) / 255
	v := []ebiten.Vertex{
		{DstX: x1, DstY: y1, SrcX: 1, SrcY: 1, ColorR: cr, ColorG: cg, ColorB: cb, ColorA: ca},
		{DstX: x2, DstY: y2, SrcX: 1, SrcY: 1, ColorR: cr, ColorG: cg, ColorB: cb, ColorA: ca},
		{DstX: x3, DstY: y3, SrcX: 1, SrcY: 1, ColorR: cr, ColorG: cg, ColorB: cb, ColorA: ca},
	}
	screen.DrawTriangles(v, []uint16{0, 1, 2}, emptySubImage, &ebiten.DrawTrianglesOptions{})
}

func drawQuad(screen *ebiten.Image, x1, y1, x2, y2, x3, y3, x4, y4 float32, c color.RGBA) {
	drawTri(screen, x1, y1, x2, y2, x3, y3, c)
	drawTri(screen, x1, y1, x3, y3, x4, y4, c)
}

func dk(c color.RGBA, f float64) color.RGBA {
	return color.RGBA{
		uint8(float64(c.R) * f),
		uint8(float64(c.G) * f),
		uint8(float64(c.B) * f),
		c.A,
	}
}

type Ghost struct {
	x, y  float64
	col   color.RGBA
	dir   int
	timer int
}

func newGhost(col, row int, c color.RGBA) *Ghost {
	return &Ghost{
		x:     float64(col)*cfg.CellSize + cfg.CellSize/2,
		y:     float64(row)*cfg.CellSize + cfg.CellSize/2,
		col:   c,
		dir:   2,
		timer: 60,
	}
}

type Game struct {
	tiltX, tiltZ   float64
	tiltVX, tiltVZ float64
	camYaw         float64

	pacX, pacY   float64
	pacVX, pacVY float64
	pacAngle     float64
	pacMouthDir  float64

	pacStartX, pacStartY float64

	ghosts []*Ghost

	cells   [][]byte
	pellets int
	score   int
	lives   int

	gameOver     bool
	win          bool
	respawnTimer int
}

func NewGame() *Game {
	g := &Game{lives: 3, pacMouthDir: 1, camYaw: math.Pi}
	g.initMaze()
	return g
}

func (g *Game) initMaze() {
	g.pellets = 0
	g.ghosts = nil
	g.cells = make([][]byte, mazeH)
	for i := range g.cells {
		g.cells[i] = make([]byte, mazeW)
	}

	ghostColors := []color.RGBA{
		{255, 0, 0, 255},
		{255, 180, 255, 255},
		{0, 220, 220, 255},
		{255, 165, 0, 255},
	}
	ghostIdx := 0

	for row := 0; row < mazeH; row++ {
		line := ""
		if row < len(mazeLayout) {
			line = mazeLayout[row]
		}
		for col := 0; col < mazeW; col++ {
			var ch byte = ' '
			if col < len(line) {
				ch = line[col]
			}
			switch ch {
			case '#':
				g.cells[row][col] = '#'
			case 'P':
				g.cells[row][col] = ' '
				g.pacStartX = float64(col)*cfg.CellSize + cfg.CellSize/2
				g.pacStartY = float64(row)*cfg.CellSize + cfg.CellSize/2
			case 'G':
				g.cells[row][col] = ' '
				if ghostIdx < len(ghostColors) {
					g.ghosts = append(g.ghosts, newGhost(col, row, ghostColors[ghostIdx]))
					ghostIdx++
				}
			case '.':
				g.cells[row][col] = '.'
				g.pellets++
			case 'o':
				g.cells[row][col] = 'o'
				g.pellets++
			default:
				g.cells[row][col] = ' '
			}
		}
	}

	g.pacX = g.pacStartX
	g.pacY = g.pacStartY
	g.pacVX = 0
	g.pacVY = 0
}

func (g *Game) wall(col, row int) bool {
	if col < 0 || col >= mazeW || row < 0 || row >= mazeH {
		return true
	}
	return g.cells[row][col] == '#'
}

func (g *Game) wallAt(wx, wz float64) bool {
	return g.wall(int(wx/cfg.CellSize), int(wz/cfg.CellSize))
}

func (g *Game) updatePacman(dt float64) {
	g.pacVX += cfg.Gravity * math.Sin(-g.tiltZ) * dt
	g.pacVY += cfg.Gravity * math.Sin(g.tiltX) * dt
	g.pacVX *= cfg.Friction
	g.pacVY *= cfg.Friction

	spd := math.Hypot(g.pacVX, g.pacVY)
	if spd > cfg.MaxSpeed {
		g.pacVX = g.pacVX / spd * cfg.MaxSpeed
		g.pacVY = g.pacVY / spd * cfg.MaxSpeed
	}

	nx := g.pacX + g.pacVX*dt
	if !g.wallAt(nx-cfg.PacRadius+1, g.pacY) && !g.wallAt(nx+cfg.PacRadius-1, g.pacY) {
		g.pacX = nx
	} else {
		g.pacVX *= -0.25
	}

	nz := g.pacY + g.pacVY*dt
	if !g.wallAt(g.pacX, nz-cfg.PacRadius+1) && !g.wallAt(g.pacX, nz+cfg.PacRadius-1) {
		g.pacY = nz
	} else {
		g.pacVY *= -0.25
	}

	g.pacAngle += g.pacMouthDir * 3.5 * dt
	if g.pacAngle > 0.42 {
		g.pacMouthDir = -1
	}
	if g.pacAngle < 0.02 {
		g.pacMouthDir = 1
	}

	col := int(g.pacX / cfg.CellSize)
	row := int(g.pacY / cfg.CellSize)
	if row >= 0 && row < mazeH && col >= 0 && col < mazeW {
		switch g.cells[row][col] {
		case '.':
			g.cells[row][col] = ' '
			g.score += 10
			g.pellets--
		case 'o':
			g.cells[row][col] = ' '
			g.score += 50
			g.pellets--
		}
	}

	if g.pellets <= 0 {
		g.win = true
	}
}

var gDirs = [][2]float64{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

func (g *Game) updateGhosts(dt float64) {
	for _, gh := range g.ghosts {
		gh.timer--
		if gh.timer <= 0 {
			gh.timer = cfg.GhostTimer
			best, bestD := -1, math.MaxFloat64
			for i, d := range gDirs {
				nx := gh.x + d[0]*cfg.CellSize
				ny := gh.y + d[1]*cfg.CellSize
				if g.wallAt(nx, ny) {
					continue
				}
				dist := math.Hypot(nx-g.pacX, ny-g.pacY)
				if dist < bestD {
					bestD = dist
					best = i
				}
			}
			if best >= 0 {
				gh.dir = best
			}
		}
		d := gDirs[gh.dir]
		nx := gh.x + d[0]*cfg.GhostSpeed*dt
		ny := gh.y + d[1]*cfg.GhostSpeed*dt
		if !g.wallAt(nx, ny) {
			gh.x = nx
			gh.y = ny
		} else {
			gh.timer = 0
		}

		if math.Hypot(gh.x-g.pacX, gh.y-g.pacY) < cfg.CellSize*0.72 {
			g.lives--
			if g.lives <= 0 {
				g.gameOver = true
				return
			}
			g.respawnTimer = 150
			g.pacVX = 0
			g.pacVY = 0
			g.pacX = g.pacStartX
			g.pacY = g.pacStartY
		}
	}
}

func (g *Game) updateTilt() {
	const maxTV = 0.028
	if ebiten.IsKeyPressed(ebiten.KeyArrowRight) {
		g.tiltVZ += cfg.TiltSpeed
	} else if ebiten.IsKeyPressed(ebiten.KeyArrowLeft) {
		g.tiltVZ -= cfg.TiltSpeed
	} else {
		g.tiltVZ *= cfg.TiltDamp
	}
	if ebiten.IsKeyPressed(ebiten.KeyArrowDown) {
		g.tiltVX += cfg.TiltSpeed
	} else if ebiten.IsKeyPressed(ebiten.KeyArrowUp) {
		g.tiltVX -= cfg.TiltSpeed
	} else {
		g.tiltVX *= cfg.TiltDamp
	}
	g.tiltVX = math.Max(-maxTV, math.Min(maxTV, g.tiltVX))
	g.tiltVZ = math.Max(-maxTV, math.Min(maxTV, g.tiltVZ))
	g.tiltX = math.Max(-cfg.MaxTilt, math.Min(cfg.MaxTilt, g.tiltX+g.tiltVX))
	g.tiltZ = math.Max(-cfg.MaxTilt, math.Min(cfg.MaxTilt, g.tiltZ+g.tiltVZ))
}

func (g *Game) updateCamera() {
	if ebiten.IsKeyPressed(ebiten.KeyQ) {
		g.camYaw -= cfg.CamYawSpeed
	}
	if ebiten.IsKeyPressed(ebiten.KeyE) {
		g.camYaw += cfg.CamYawSpeed
	}
}

func (g *Game) Update() error {
	if ebiten.IsKeyPressed(ebiten.KeyR) {
		yaw := g.camYaw
		*g = *NewGame()
		g.camYaw = yaw
		return nil
	}
	if g.gameOver || g.win {
		return nil
	}
	g.updateTilt()
	g.updateCamera()
	if g.respawnTimer > 0 {
		g.respawnTimer--
		return nil
	}
	const dt = 1.0 / 60.0
	g.updatePacman(dt)
	g.updateGhosts(dt)
	return nil
}

type rItem struct {
	depth float64
	draw  func()
}

func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{8, 8, 18, 255})

	tx, tz, yaw := g.tiltX, g.tiltZ, g.camYaw
	items := make([]rItem, 0, 800)

	wTop := color.RGBA{40, 100, 230, 255}
	wNear := color.RGBA{20, 55, 125, 255}
	wFar := dk(wNear, 0.75)
	wLeft := color.RGBA{25, 65, 150, 255}
	wRight := color.RGBA{32, 80, 190, 255}
	flA := color.RGBA{24, 24, 55, 255}
	flB := color.RGBA{18, 18, 42, 255}
	pelC := color.RGBA{255, 255, 140, 255}

	cs := cfg.CellSize
	wh := cfg.WallH

	for row := 0; row < mazeH; row++ {
		for col := 0; col < mazeW; col++ {
			x0 := float64(col) * cs
			x1 := x0 + cs
			z0 := float64(row) * cs
			z1 := z0 + cs
			dep := cellDepth(x0, x1, z0, z1, yaw)
			r, c := row, col

			if g.cells[row][col] == '#' {
				items = append(items, rItem{dep + 20, func() {
					ax, ay := project(x0, wh, z0, tx, tz, yaw)
					bx, by := project(x1, wh, z0, tx, tz, yaw)
					cx2, cy := project(x1, wh, z1, tx, tz, yaw)
					dx, dy := project(x0, wh, z1, tx, tz, yaw)
					drawQuad(screen, ax, ay, bx, by, cx2, cy, dx, dy, wTop)
				}})
				items = append(items, rItem{dep + 11, func() {
					ax, ay := project(x0, 0, z1, tx, tz, yaw)
					bx, by := project(x1, 0, z1, tx, tz, yaw)
					cx2, cy := project(x1, wh, z1, tx, tz, yaw)
					dx, dy := project(x0, wh, z1, tx, tz, yaw)
					drawQuad(screen, ax, ay, bx, by, cx2, cy, dx, dy, wNear)
				}})
				items = append(items, rItem{dep + 9, func() {
					ax, ay := project(x0, 0, z0, tx, tz, yaw)
					bx, by := project(x1, 0, z0, tx, tz, yaw)
					cx2, cy := project(x1, wh, z0, tx, tz, yaw)
					dx, dy := project(x0, wh, z0, tx, tz, yaw)
					drawQuad(screen, ax, ay, bx, by, cx2, cy, dx, dy, wFar)
				}})
				items = append(items, rItem{dep + 10, func() {
					ax, ay := project(x1, 0, z0, tx, tz, yaw)
					bx, by := project(x1, 0, z1, tx, tz, yaw)
					cx2, cy := project(x1, wh, z1, tx, tz, yaw)
					dx, dy := project(x1, wh, z0, tx, tz, yaw)
					drawQuad(screen, ax, ay, bx, by, cx2, cy, dx, dy, wRight)
				}})
				items = append(items, rItem{dep + 10, func() {
					ax, ay := project(x0, 0, z0, tx, tz, yaw)
					bx, by := project(x0, 0, z1, tx, tz, yaw)
					cx2, cy := project(x0, wh, z1, tx, tz, yaw)
					dx, dy := project(x0, wh, z0, tx, tz, yaw)
					drawQuad(screen, ax, ay, bx, by, cx2, cy, dx, dy, wLeft)
				}})
			} else {
				fc := flA
				if (r+c)%2 == 0 {
					fc = flB
				}
				items = append(items, rItem{dep, func() {
					ax, ay := project(x0, 0, z0, tx, tz, yaw)
					bx, by := project(x1, 0, z0, tx, tz, yaw)
					cx2, cy := project(x1, 0, z1, tx, tz, yaw)
					dx, dy := project(x0, 0, z1, tx, tz, yaw)
					drawQuad(screen, ax, ay, bx, by, cx2, cy, dx, dy, fc)
				}})
				if g.cells[r][c] == '.' {
					pcx := (x0 + x1) / 2
					pcz := (z0 + z1) / 2
					items = append(items, rItem{dep + 3, func() {
						px, py := project(pcx, 3, pcz, tx, tz, yaw)
						vector.DrawFilledCircle(screen, px, py, 3, pelC, false)
					}})
				}
			}
		}
	}

	for _, gh := range g.ghosts {
		gx, gy, gc := gh.x, gh.y, gh.col
		gd := cellDepth(gx, gx, gy, gy, yaw)
		items = append(items, rItem{gd + 6, func() {
			bx, by := project(gx, 16, gy, tx, tz, yaw)
			vector.DrawFilledCircle(screen, bx, by, 10, gc, false)
			lx, ly := project(gx, 4, gy, tx, tz, yaw)
			vector.DrawFilledRect(screen, lx-10, ly-3, 20, 12, gc, false)
			e1x, e1y := project(gx-3.5, 20, gy, tx, tz, yaw)
			e2x, e2y := project(gx+3.5, 20, gy, tx, tz, yaw)
			vector.DrawFilledCircle(screen, e1x, e1y, 3, color.White, false)
			vector.DrawFilledCircle(screen, e2x, e2y, 3, color.White, false)
			vector.DrawFilledCircle(screen, e1x+1, e1y+1, 1.5, color.RGBA{0, 0, 200, 255}, false)
			vector.DrawFilledCircle(screen, e2x+1, e2y+1, 1.5, color.RGBA{0, 0, 200, 255}, false)
		}})
	}

	if g.respawnTimer == 0 || (g.respawnTimer/8)%2 == 0 {
		px, py := g.pacX, g.pacY
		vx, vy := g.pacVX, g.pacVY
		pa := g.pacAngle
		pr := cfg.PacRadius
		pd := cellDepth(px, px, py, py, yaw)
		items = append(items, rItem{pd + 9, func() {
			sx, sy := project(px, pr+1, py, tx, tz, yaw)
			facing := 0.0
			if math.Hypot(vx, vy) > 4 {
				facing = math.Atan2(vy, vx)
			}
			vector.DrawFilledCircle(screen, sx, sy, float32(pr), color.RGBA{255, 220, 0, 255}, false)
			a1 := facing + pa
			a2 := facing - pa
			r32 := float32(pr + 1)
			m1x := sx + float32(math.Cos(a1))*r32
			m1y := sy + float32(math.Sin(a1))*r32
			m2x := sx + float32(math.Cos(a2))*r32
			m2y := sy + float32(math.Sin(a2))*r32
			drawTri(screen, sx, sy, m1x, m1y, m2x, m2y, color.RGBA{8, 8, 18, 255})
		}})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].depth < items[j].depth })
	for _, it := range items {
		it.draw()
	}

	ebitenutil.DebugPrint(screen, fmt.Sprintf(
		"Score: %d   Lives: %d   Pellets: %d\nTilt ←→: %.1f°  ↑↓: %.1f°   Cam: %.0f°\n[←→↑↓] Наклон  [Q/E] Камера  [R] Рестарт",
		g.score, g.lives, g.pellets,
		g.tiltZ*180/math.Pi, g.tiltX*180/math.Pi,
		math.Mod(g.camYaw*180/math.Pi, 360),
	))

	g.drawTiltHUD(screen)

	if g.gameOver {
		ebitenutil.DebugPrintAt(screen, "   GAME OVER   нажмите R", screenW/2-90, screenH/2)
	}
	if g.win {
		ebitenutil.DebugPrintAt(screen, "   ПОБЕДА!   нажмите R", screenW/2-80, screenH/2)
	}
}

func (g *Game) drawTiltHUD(screen *ebiten.Image) {
	cx := float32(screenW - 65)
	cy := float32(screenH - 65)
	r := float32(40)
	vector.StrokeCircle(screen, cx, cy, r, 2, color.RGBA{70, 70, 110, 200}, false)
	vector.StrokeLine(screen, cx-r, cy, cx+r, cy, 1, color.RGBA{50, 50, 80, 160}, false)
	vector.StrokeLine(screen, cx, cy-r, cx, cy+r, 1, color.RGBA{50, 50, 80, 160}, false)
	bx := cx + float32(g.tiltZ/cfg.MaxTilt)*r*0.82
	by := cy + float32(g.tiltX/cfg.MaxTilt)*r*0.82
	vector.DrawFilledCircle(screen, bx, by, 9, color.RGBA{255, 220, 0, 240}, false)
}

func (g *Game) Layout(_, _ int) (int, int) { return screenW, screenH }

func main() {
	loadConfig("config.json")
	loadMaze("maze.txt")

	ebiten.SetWindowSize(screenW, screenH)
	ebiten.SetWindowTitle("3D Pac-Man — Наклонная платформа")
	ebiten.SetTPS(60)
	if err := ebiten.RunGame(NewGame()); err != nil {
		log.Fatal(err)
	}
}
