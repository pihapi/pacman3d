package main

import (
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"sort"

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
	screenW   = 900
	screenH   = 700
	cellSize  = 34.0
	wallH     = 22.0
	maxTilt   = 0.4
	tiltSpeed = 0.011
	tiltDamp  = 0.86
	grav      = 1400.0
	friction  = 0.85
	pacRadius = 10.0
)

var maze = []string{
	"####################",
	"#........##........#",
	"#.##.###.##.###.##.#",
	"#.##.###.##.###.##.#",
	"#..................#",
	"#.##.#.######.#.##.#",
	"#....#...##...#....#",
	"####.###.##.###.####",
	"#..................#",
	"#.##.##.####.##.##.#",
	"#....##.####.##....#",
	"#.##....GGGG....##.#",
	"#.##.##.####.##.##.#",
	"#..................#",
	"####.###.##.###.####",
	"#....#...##...#....#",
	"#.##.#.######.#.##.#",
	"#..................#",
	"#.##.###.##.###.##.#",
	"#.......P..........#",
	"####################",
}

const (
	mazeW = 20
	mazeH = 21
)

var (
	boardCX = float64(mazeW) * cellSize / 2
	boardCZ = float64(mazeH) * cellSize / 2
	camH    = 500.0
	camD    = 340.0
	camFovD = 420.0
)

func project(wx, wy, wz, tiltX, tiltZ float64) (float32, float32) {
	rx := wx - boardCX
	ry := wy
	rz := wz - boardCZ

	cosX, sinX := math.Cos(tiltX), math.Sin(tiltX)
	ry1 := ry*cosX - rz*sinX
	rz1 := ry*sinX + rz*cosX

	cosZ, sinZ := math.Cos(tiltZ), math.Sin(tiltZ)
	rx1 := rx*cosZ - ry1*sinZ
	ry2 := rx*sinZ + ry1*cosZ

	wx2 := rx1 + boardCX
	wy2 := ry2
	wz2 := rz1 + boardCZ

	vx := wx2 - boardCX
	vy := wy2 - camH
	vz := wz2 - (boardCZ + camD)

	fLen := math.Sqrt(camH*camH + camD*camD)
	fwdY := -camH / fLen
	fwdZ := -camD / fLen
	upY := -fwdZ
	upZ := fwdY

	csx := vx
	csy := vy*upY + vz*upZ
	csz := vy*fwdY + vz*fwdZ

	if csz < 1 {
		csz = 1
	}
	scale := camFovD / csz
	return float32(csx*scale + screenW/2), float32(-csy*scale + screenH/2 + 20)
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
	return color.RGBA{uint8(float64(c.R) * f), uint8(float64(c.G) * f), uint8(float64(c.B) * f), c.A}
}

type Ghost struct {
	x, y  float64
	col   color.RGBA
	dir   int
	timer int
}

func newGhost(col, row int, c color.RGBA) *Ghost {
	return &Ghost{
		x:     float64(col)*cellSize + cellSize/2,
		y:     float64(row)*cellSize + cellSize/2,
		col:   c,
		dir:   2,
		timer: 60,
	}
}

type Game struct {
	tiltX, tiltZ   float64
	tiltVX, tiltVZ float64

	pacX, pacY   float64
	pacVX, pacVY float64
	pacAngle     float64
	pacMouthDir  float64

	ghosts []*Ghost

	cells   [mazeH][mazeW]byte
	pellets int
	score   int
	lives   int

	gameOver     bool
	win          bool
	respawnTimer int

	pacStartX, pacStartY float64
}

func NewGame() *Game {
	g := &Game{lives: 3, pacMouthDir: 1}
	g.initMaze()
	return g
}

func (g *Game) initMaze() {
	g.pellets = 0
	g.ghosts = nil

	ghostColors := []color.RGBA{
		{255, 0, 0, 255},
		{255, 180, 255, 255},
		{0, 220, 220, 255},
		{255, 165, 0, 255},
	}
	ghostIdx := 0

	for row := 0; row < mazeH; row++ {
		line := ""
		if row < len(maze) {
			line = maze[row]
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
				g.pacStartX = float64(col)*cellSize + cellSize/2
				g.pacStartY = float64(row)*cellSize + cellSize/2
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
	return g.wall(int(wx/cellSize), int(wz/cellSize))
}

func (g *Game) updatePacman(dt float64) {
	g.pacVX += grav * math.Sin(-g.tiltZ) * dt // минус у tiltZ
	g.pacVY += grav * math.Sin(g.tiltX) * dt  // tiltX без минуса
	g.pacVX *= friction
	g.pacVY *= friction

	spd := math.Hypot(g.pacVX, g.pacVY)
	if spd > 380 {
		g.pacVX = g.pacVX / spd * 380
		g.pacVY = g.pacVY / spd * 380
	}

	nx := g.pacX + g.pacVX*dt
	if !g.wallAt(nx-pacRadius+1, g.pacY) && !g.wallAt(nx+pacRadius-1, g.pacY) {
		g.pacX = nx
	} else {
		g.pacVX *= -0.25
	}

	nz := g.pacY + g.pacVY*dt
	if !g.wallAt(g.pacX, nz-pacRadius+1) && !g.wallAt(g.pacX, nz+pacRadius-1) {
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

	col := int(g.pacX / cellSize)
	row := int(g.pacY / cellSize)
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
	const spd = 42.0
	for _, gh := range g.ghosts {
		gh.timer--
		if gh.timer <= 0 {
			gh.timer = 55
			best, bestD := -1, math.MaxFloat64
			for i, d := range gDirs {
				nx := gh.x + d[0]*cellSize
				ny := gh.y + d[1]*cellSize
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
		nx := gh.x + d[0]*spd*dt
		ny := gh.y + d[1]*spd*dt
		if !g.wallAt(nx, ny) {
			gh.x = nx
			gh.y = ny
		} else {
			gh.timer = 0
		}

		if math.Hypot(gh.x-g.pacX, gh.y-g.pacY) < cellSize*0.72 {
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
		g.tiltVZ += tiltSpeed
	} else if ebiten.IsKeyPressed(ebiten.KeyArrowLeft) {
		g.tiltVZ -= tiltSpeed
	} else {
		g.tiltVZ *= tiltDamp
	}

	if ebiten.IsKeyPressed(ebiten.KeyArrowDown) {
		g.tiltVX += tiltSpeed
	} else if ebiten.IsKeyPressed(ebiten.KeyArrowUp) {
		g.tiltVX -= tiltSpeed
	} else {
		g.tiltVX *= tiltDamp
	}

	g.tiltVX = math.Max(-maxTV, math.Min(maxTV, g.tiltVX))
	g.tiltVZ = math.Max(-maxTV, math.Min(maxTV, g.tiltVZ))
	g.tiltX = math.Max(-maxTilt, math.Min(maxTilt, g.tiltX+g.tiltVX))
	g.tiltZ = math.Max(-maxTilt, math.Min(maxTilt, g.tiltZ+g.tiltVZ))
}

func (g *Game) Update() error {
	if ebiten.IsKeyPressed(ebiten.KeyR) {
		*g = *NewGame()
		return nil
	}
	if g.gameOver || g.win {
		return nil
	}
	g.updateTilt()
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

	tx, tz := g.tiltX, g.tiltZ
	items := make([]rItem, 0, 700)

	wTop := color.RGBA{40, 100, 230, 255}
	wFront := color.RGBA{20, 55, 125, 255}
	wSide := color.RGBA{28, 72, 175, 255}
	flA := color.RGBA{24, 24, 55, 255}
	flB := color.RGBA{18, 18, 42, 255}
	pelC := color.RGBA{255, 255, 140, 255}

	for row := 0; row < mazeH; row++ {
		for col := 0; col < mazeW; col++ {
			x0 := float64(col) * cellSize
			x1 := x0 + cellSize
			z0 := float64(row) * cellSize
			z1 := z0 + cellSize
			dep := z0
			r, c := row, col

			if g.cells[row][col] == '#' {
				items = append(items, rItem{dep + 20, func() {
					ax, ay := project(x0, wallH, z0, tx, tz)
					bx, by := project(x1, wallH, z0, tx, tz)
					cx2, cy := project(x1, wallH, z1, tx, tz)
					dx, dy := project(x0, wallH, z1, tx, tz)
					drawQuad(screen, ax, ay, bx, by, cx2, cy, dx, dy, wTop)
				}})
				items = append(items, rItem{dep + 10, func() {
					ax, ay := project(x0, 0, z1, tx, tz)
					bx, by := project(x1, 0, z1, tx, tz)
					cx2, cy := project(x1, wallH, z1, tx, tz)
					dx, dy := project(x0, wallH, z1, tx, tz)
					drawQuad(screen, ax, ay, bx, by, cx2, cy, dx, dy, wFront)
				}})
				items = append(items, rItem{dep + 5, func() {
					ax, ay := project(x1, 0, z0, tx, tz)
					bx, by := project(x1, 0, z1, tx, tz)
					cx2, cy := project(x1, wallH, z1, tx, tz)
					dx, dy := project(x1, wallH, z0, tx, tz)
					drawQuad(screen, ax, ay, bx, by, cx2, cy, dx, dy, wSide)
				}})
				items = append(items, rItem{dep + 4, func() {
					ax, ay := project(x0, 0, z0, tx, tz)
					bx, by := project(x1, 0, z0, tx, tz)
					cx2, cy := project(x1, wallH, z0, tx, tz)
					dx, dy := project(x0, wallH, z0, tx, tz)
					drawQuad(screen, ax, ay, bx, by, cx2, cy, dx, dy, dk(wFront, 0.7))
				}})
			} else {
				fc := flA
				if (r+c)%2 == 0 {
					fc = flB
				}
				items = append(items, rItem{dep, func() {
					ax, ay := project(x0, 0, z0, tx, tz)
					bx, by := project(x1, 0, z0, tx, tz)
					cx2, cy := project(x1, 0, z1, tx, tz)
					dx, dy := project(x0, 0, z1, tx, tz)
					drawQuad(screen, ax, ay, bx, by, cx2, cy, dx, dy, fc)
				}})
				if g.cells[r][c] == '.' {
					pcx := (x0 + x1) / 2
					pcz := (z0 + z1) / 2
					items = append(items, rItem{dep + 3, func() {
						px, py := project(pcx, 3, pcz, tx, tz)
						vector.DrawFilledCircle(screen, px, py, 3, pelC, false)
					}})
				}
			}
		}
	}

	for _, gh := range g.ghosts {
		gx, gy, gc := gh.x, gh.y, gh.col
		items = append(items, rItem{gy + 6, func() {
			bx, by := project(gx, 16, gy, tx, tz)
			vector.DrawFilledCircle(screen, bx, by, 10, gc, false)
			lx, ly := project(gx, 4, gy, tx, tz)
			vector.DrawFilledRect(screen, lx-10, ly-3, 20, 12, gc, false)
			e1x, e1y := project(gx-3.5, 20, gy, tx, tz)
			e2x, e2y := project(gx+3.5, 20, gy, tx, tz)
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
		items = append(items, rItem{py + 9, func() {
			sx, sy := project(px, pacRadius+1, py, tx, tz)
			facing := 0.0
			if math.Hypot(vx, vy) > 4 {
				facing = math.Atan2(vy, vx)
			}
			vector.DrawFilledCircle(screen, sx, sy, float32(pacRadius), color.RGBA{255, 220, 0, 255}, false)
			a1 := facing + pa
			a2 := facing - pa
			r32 := float32(pacRadius + 1)
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
		"Score: %d   Lives: %d   Pellets: %d\nTilt ←→: %.1f°   Tilt ↑↓: %.1f°\n[←→↑↓] Наклон   [R] Рестарт",
		g.score, g.lives, g.pellets,
		g.tiltZ*180/math.Pi, g.tiltX*180/math.Pi,
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
	bx := cx + float32(g.tiltZ/maxTilt)*r*0.82
	by := cy + float32(g.tiltX/maxTilt)*r*0.82
	vector.DrawFilledCircle(screen, bx, by, 9, color.RGBA{255, 220, 0, 240}, false)
}

func (g *Game) Layout(_, _ int) (int, int) { return screenW, screenH }

func main() {
	ebiten.SetWindowSize(screenW, screenH)
	ebiten.SetWindowTitle("3D Pac-Man — Наклонная платформа")
	ebiten.SetTPS(60)
	if err := ebiten.RunGame(NewGame()); err != nil {
		log.Fatal(err)
	}
}
