// Package calendar fa i conti con i giorni lavorativi: sabato, domenica e festività
// nazionali italiane (incluso il lunedì dell'Angelo) non contano. Le date sono giorni
// di calendario in UTC a mezzanotte.
package calendar

import (
	"sync"
	"time"
)

// Day normalizza un istante al giorno (UTC, mezzanotte).
func Day(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Parse legge una data AAAA-MM-GG.
func Parse(s string) (time.Time, error) { return time.Parse(time.DateOnly, s) }

// easter restituisce la domenica di Pasqua (algoritmo anonimo gregoriano).
func easter(y int) time.Time {
	a := y % 19
	b, c := y/100, y%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(y, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

var fixed = [][2]int{{1, 1}, {1, 6}, {4, 25}, {5, 1}, {6, 2}, {8, 15}, {11, 1}, {12, 8}, {12, 25}, {12, 26}}

// angelo: giorno (mese, giorno) del lunedì dell'Angelo per anno. Holiday gira per ogni giorno
// di ogni conteggio (Late e Workdays, 600 voci in una inbox): il calcolo della Pasqua, rifatto
// a ogni giorno, era il 25% della CPU del benchmark del 2026-11-24.
var angelo sync.Map // int -> [2]int

func easterMonday(y int) [2]int {
	if v, ok := angelo.Load(y); ok {
		return v.([2]int)
	}
	t := easter(y).AddDate(0, 0, 1)
	md := [2]int{int(t.Month()), t.Day()}
	angelo.Store(y, md)
	return md
}

// Holiday dice se il giorno è una festività nazionale italiana.
func Holiday(t time.Time) bool {
	y, mo, d := t.UTC().Date()
	for _, f := range fixed {
		if int(mo) == f[0] && d == f[1] {
			return true
		}
	}
	md := easterMonday(y)
	return int(mo) == md[0] && d == md[1]
}

// Workday dice se il giorno è lavorativo.
func Workday(t time.Time) bool {
	wd := t.Weekday()
	return wd != time.Saturday && wd != time.Sunday && !Holiday(t)
}

// NextWorkday restituisce il giorno stesso se lavorativo, altrimenti il primo lavorativo successivo.
func NextWorkday(t time.Time) time.Time {
	t = Day(t)
	for !Workday(t) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// EndAfter restituisce l'ultimo giorno di un'attività che inizia il giorno start
// (portato al primo lavorativo) e dura n giorni lavorativi (almeno 1).
func EndAfter(start time.Time, n int) time.Time {
	if n < 1 {
		n = 1
	}
	t := NextWorkday(start)
	for n > 1 {
		t = t.AddDate(0, 0, 1)
		if Workday(t) {
			n--
		}
	}
	return t
}

// Workdays conta i giorni lavorativi da a a b inclusi (0 se b è prima di a).
func Workdays(a, b time.Time) int {
	a, b = Day(a), Day(b)
	n := 0
	for t := a; !t.After(b); t = t.AddDate(0, 0, 1) {
		if Workday(t) {
			n++
		}
	}
	return n
}

// AfterEnd è il primo giorno lavorativo dopo la fine di un'attività.
func AfterEnd(end time.Time) time.Time { return NextWorkday(Day(end).AddDate(0, 0, 1)) }

// AddWorkdays restituisce il giorno lavorativo che cade n giorni lavorativi dopo t
// (t escluso): con n = 5 una consegna di lunedì scade il lunedì successivo. Con n <= 0
// restituisce t se è lavorativo, altrimenti il primo lavorativo successivo.
func AddWorkdays(t time.Time, n int) time.Time {
	t = Day(t)
	if n <= 0 {
		return NextWorkday(t)
	}
	for n > 0 {
		t = t.AddDate(0, 0, 1)
		if Workday(t) {
			n--
		}
	}
	return t
}

// Late conta i giorni lavorativi di ritardo di oggi rispetto alla scadenza (0 se non è scaduta).
func Late(due, today time.Time) int {
	due, today = Day(due), Day(today)
	if !today.After(due) {
		return 0
	}
	return Workdays(due.AddDate(0, 0, 1), today)
}
