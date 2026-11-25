package devsecops

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// La politica applicata a un'applicazione: serve a generare la configurazione dei repository
// (adozione guidata, connettore GitHub).

// PolicyControl è un controllo con la modalità richiesta all'applicazione.
type PolicyControl struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Mode string `json:"mode"` // off | warn | block
}

// AppPolicy è ciò che la politica richiede a un'applicazione.
type AppPolicy struct {
	Configured bool            `json:"configured"`
	Version    int             `json:"version"`
	AppID      string          `json:"app_id"`
	AppKey     string          `json:"app_key"`
	AppName    string          `json:"app_name"`
	AppCode    *string         `json:"app_code"`
	Class      string          `json:"class"`
	ClassKey   string          `json:"class_key"`
	Controls   []PolicyControl `json:"controls"`
}

// PolicyFor risolve la classe dell'applicazione e i controlli richiesti. Senza una politica
// salvata Configured è false e i controlli sono vuoti.
func PolicyFor(ctx context.Context, tx pgx.Tx, appID string) (*AppPolicy, error) {
	def, v, err := LoadPolicy(ctx, tx)
	if err != nil {
		return nil, err
	}
	exposure := ""
	if def != nil {
		exposure = def.ExposureField
	}
	apps, _, err := loadFleet(ctx, tx, exposure, []string{appID})
	if err != nil {
		return nil, err
	}
	if len(apps) == 0 {
		return nil, engine.ErrNotFound
	}
	a := apps[0]
	p := &AppPolicy{AppID: a.ID, AppKey: a.Key, AppName: a.Name, AppCode: a.Code, Controls: []PolicyControl{}}
	if def == nil {
		return p, nil
	}
	class := def.ClassOf(a.Attrs)
	p.Configured, p.Version, p.Class, p.ClassKey = true, v, class.Name, class.Key
	for _, c := range def.Controls {
		p.Controls = append(p.Controls, PolicyControl{Key: c.Key, Name: c.Name, Mode: orOff(class.Modes[c.Key])})
	}
	return p, nil
}
