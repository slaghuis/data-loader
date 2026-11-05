package sink

import (
	"testing"

	"github.com/slaghuis/data-loader/pkg/contracts"
)

func TestStagingTarget_Suffix_ADR020(t *testing.T) {
	got, err := stagingTarget(contracts.TargetTable{Schema: "dbo", Name: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != "dbo" || got.Name != "orders_stg" {
		t.Fatalf("got %+v", got)
	}
}

func TestOldTarget_Suffix_ADR020(t *testing.T) {
	got, err := oldTarget(contracts.TargetTable{Schema: "dbo", Name: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "orders_old" {
		t.Fatalf("got %q", got.Name)
	}
}
