package tariff

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/tariff/tibber"
	"github.com/evcc-io/evcc/util"
	"github.com/evcc-io/evcc/util/request"
	"github.com/shurcooL/graphql"
	"golang.org/x/oauth2"
)

type Tibber struct {
	mux    sync.Mutex
	log    *util.Logger
	Token  string
	HomeID string
	Cheap  float64
	client *graphql.Client
	data   []tibber.PriceInfo
}

var _ api.Tariff = (*Tibber)(nil)

func NewTibber(other map[string]interface{}) (*Tibber, error) {
	t := &Tibber{}

	if err := util.DecodeOther(other, &t); err != nil {
		return nil, err
	}

	t.log = util.NewLogger("tibber").Redact(t.HomeID, t.Token)

	ctx := context.WithValue(
		context.Background(),
		oauth2.HTTPClient,
		request.NewHelper(t.log).Client,
	)

	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: t.Token,
	}))

	t.client = graphql.NewClient(tibber.URI, client)

	if t.HomeID == "" {
		var err error
		if t.HomeID, err = t.DefaultHomeID(); err != nil {
			return nil, err
		}
	}

	go t.Run()

	return t, nil
}

func (t *Tibber) DefaultHomeID() (string, error) {
	var res struct {
		Viewer struct {
			Homes []tibber.Home
		}
	}

	if err := t.client.Query(context.Background(), &res, nil); err != nil {
		return "", err
	}

	if len(res.Viewer.Homes) != 1 {
		return "", fmt.Errorf("could not determine home id: %v", res.Viewer.Homes)
	}

	return res.Viewer.Homes[0].ID, nil
}

func (t *Tibber) Run() {
	for ; true; <-time.NewTicker(time.Hour).C {
		var res struct {
			Viewer struct {
				Home struct {
					ID                  string
					TimeZone            string
					CurrentSubscription tibber.Subscription
				} `graphql:"home(id: $id)"`
			}
		}

		v := map[string]interface{}{
			"id": graphql.ID(t.HomeID),
		}

		if err := t.client.Query(context.Background(), &res, v); err != nil {
			t.log.ERROR.Println(err)
			continue
		}

		t.mux.Lock()
		t.data = res.Viewer.Home.CurrentSubscription.PriceInfo.Today
		t.mux.Unlock()
	}
}

func (t *Tibber) CurrentPrice() (float64, error) {
	t.mux.Lock()
	defer t.mux.Unlock()

	for i := len(t.data) - 1; i >= 0; i-- {
		pi := t.data[i]

		if pi.StartsAt.Before(time.Now()) {
			return pi.Total, nil
		}
	}
	return 0, errors.New("unable to find current tibber price")
}

func (t *Tibber) IsCheap() (bool, error) {
	price, err := t.CurrentPrice()
	return price <= t.Cheap, err
}
