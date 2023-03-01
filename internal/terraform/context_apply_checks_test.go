package terraform

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/hashicorp/terraform/internal/addrs"
	"github.com/hashicorp/terraform/internal/checks"
	"github.com/hashicorp/terraform/internal/configs/configschema"
	"github.com/hashicorp/terraform/internal/plans"
	"github.com/hashicorp/terraform/internal/providers"
	"github.com/hashicorp/terraform/internal/states"
	"github.com/hashicorp/terraform/internal/tfdiags"
)

// This file contains 'integration' tests for the Terraform check blocks.
//
// These tests could live in context_apply_test or context_apply2_test but given
// the size of those files, it makes sense to keep these check related tests
// grouped together.

type checksTestingStatus struct {
	status   checks.Status
	messages []string
	refs     [][]string
}

func TestContextChecks(t *testing.T) {
	tests := map[string]struct {
		configs      map[string]string
		plan         map[string]checksTestingStatus
		planError    string
		apply        map[string]checksTestingStatus
		applyError   string
		state        *states.State
		provider     *MockProvider
		providerHook func(*MockProvider)
	}{
		"passing": {
			configs: map[string]string{
				"main.tf": `
provider "checks" {}

check "passing" {
  data "checks_object" "positive" {}

  assert {
    condition     = data.checks_object.positive.number >= 0
    error_message = "negative number"
  }
}
`,
			},
			plan: map[string]checksTestingStatus{
				"passing": {
					status: checks.StatusPass,
				},
			},
			apply: map[string]checksTestingStatus{
				"passing": {
					status: checks.StatusPass,
				},
			},
			provider: &MockProvider{
				Meta: "checks",
				GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
					DataSources: map[string]providers.Schema{
						"checks_object": {
							Block: &configschema.Block{
								Attributes: map[string]*configschema.Attribute{
									"number": {
										Type:     cty.Number,
										Computed: true,
									},
								},
							},
						},
					},
				},
				ReadDataSourceFn: func(request providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
					return providers.ReadDataSourceResponse{
						State: cty.ObjectVal(map[string]cty.Value{
							"number": cty.NumberIntVal(0),
						}),
					}
				},
			},
		},
		"failing": {
			configs: map[string]string{
				"main.tf": `
provider "checks" {}

check "failing" {
  data "checks_object" "positive" {}

  assert {
    condition     = data.checks_object.positive.number >= 0
    error_message = "negative number"
  }
}
`,
			},
			plan: map[string]checksTestingStatus{
				"failing": {
					status:   checks.StatusFail,
					messages: []string{"negative number"},
					refs: [][]string{
						{
							"data.checks_object.positive",
						},
					},
				},
			},
			apply: map[string]checksTestingStatus{
				"failing": {
					status:   checks.StatusFail,
					messages: []string{"negative number"},
					refs: [][]string{
						{
							"data.checks_object.positive",
						},
					},
				},
			},
			provider: &MockProvider{
				Meta: "checks",
				GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
					DataSources: map[string]providers.Schema{
						"checks_object": {
							Block: &configschema.Block{
								Attributes: map[string]*configschema.Attribute{
									"number": {
										Type:     cty.Number,
										Computed: true,
									},
								},
							},
						},
					},
				},
				ReadDataSourceFn: func(request providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
					return providers.ReadDataSourceResponse{
						State: cty.ObjectVal(map[string]cty.Value{
							"number": cty.NumberIntVal(-1),
						}),
					}
				},
			},
		},
		"mixed": {
			configs: map[string]string{
				"main.tf": `
provider "checks" {}

check "failing" {
  data "checks_object" "neutral" {}

  assert {
    condition     = data.checks_object.neutral.number >= 0
    error_message = "negative number"
  }

  assert {
    condition = data.checks_object.neutral.number < 0
    error_message = "positive number"
  }
}
`,
			},
			plan: map[string]checksTestingStatus{
				"failing": {
					status:   checks.StatusFail,
					messages: []string{"positive number"},
					refs: [][]string{
						{
							"data.checks_object.neutral",
						},
					},
				},
			},
			apply: map[string]checksTestingStatus{
				"failing": {
					status:   checks.StatusFail,
					messages: []string{"positive number"},
					refs: [][]string{
						{
							"data.checks_object.neutral",
						},
					},
				},
			},
			provider: &MockProvider{
				Meta: "checks",
				GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
					DataSources: map[string]providers.Schema{
						"checks_object": {
							Block: &configschema.Block{
								Attributes: map[string]*configschema.Attribute{
									"number": {
										Type:     cty.Number,
										Computed: true,
									},
								},
							},
						},
					},
				},
				ReadDataSourceFn: func(request providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
					return providers.ReadDataSourceResponse{
						State: cty.ObjectVal(map[string]cty.Value{
							"number": cty.NumberIntVal(0),
						}),
					}
				},
			},
		},
		"nested data blocks reload during apply": {
			configs: map[string]string{
				"main.tf": `
provider "checks" {}

data "checks_object" "data_block" {}

check "data_block" {
  assert {
    condition     = data.checks_object.data_block.number >= 0
    error_message = "negative number"
  }
}

check "nested_data_block" {
  data "checks_object" "nested_data_block" {}

  assert {
    condition     = data.checks_object.nested_data_block.number >= 0
    error_message = "negative number"
  }
}
`,
			},
			plan: map[string]checksTestingStatus{
				"nested_data_block": {
					status:   checks.StatusFail,
					messages: []string{"negative number"},
					refs: [][]string{
						{
							"data.checks_object.nested_data_block",
						},
					},
				},
				"data_block": {
					status:   checks.StatusFail,
					messages: []string{"negative number"},
					refs: [][]string{
						{
							"data.checks_object.data_block",
						},
					},
				},
			},
			apply: map[string]checksTestingStatus{
				"nested_data_block": {
					status: checks.StatusPass,
				},
				"data_block": {
					status:   checks.StatusFail,
					messages: []string{"negative number"},
					refs: [][]string{
						{
							"data.checks_object.data_block",
						},
					},
				},
			},
			provider: &MockProvider{
				Meta: "checks",
				GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
					DataSources: map[string]providers.Schema{
						"checks_object": {
							Block: &configschema.Block{
								Attributes: map[string]*configschema.Attribute{
									"number": {
										Type:     cty.Number,
										Computed: true,
									},
								},
							},
						},
					},
				},
				ReadDataSourceFn: func(request providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
					return providers.ReadDataSourceResponse{
						State: cty.ObjectVal(map[string]cty.Value{
							"number": cty.NumberIntVal(-1),
						}),
					}
				},
			},
			providerHook: func(provider *MockProvider) {
				provider.ReadDataSourceFn = func(request providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
					// The data returned by the data sources are changing
					// between the plan and apply stage. The nested data block
					// will update to reflect this while the normal data block
					// will not detect the change.
					return providers.ReadDataSourceResponse{
						State: cty.ObjectVal(map[string]cty.Value{
							"number": cty.NumberIntVal(0),
						}),
					}
				}
			},
		},
		"returns unknown for unknown config": {
			configs: map[string]string{
				"main.tf": `
provider "checks" {}

resource "checks_object" "resource_block" {}

check "resource_block" {
  data "checks_object" "data_block" {
    id = checks_object.resource_block.id
  }

  assert {
    condition = data.checks_object.data_block.number >= 0
    error_message = "negative number"
  }
}
`,
			},
			plan: map[string]checksTestingStatus{
				"resource_block": {
					status: checks.StatusUnknown,
				},
			},
			apply: map[string]checksTestingStatus{
				"resource_block": {
					status: checks.StatusPass,
				},
			},
			provider: &MockProvider{
				Meta: "checks",
				GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
					ResourceTypes: map[string]providers.Schema{
						"checks_object": {
							Block: &configschema.Block{
								Attributes: map[string]*configschema.Attribute{
									"id": {
										Type:     cty.String,
										Computed: true,
									},
								},
							},
						},
					},
					DataSources: map[string]providers.Schema{
						"checks_object": {
							Block: &configschema.Block{
								Attributes: map[string]*configschema.Attribute{
									"id": {
										Type:     cty.String,
										Required: true,
									},
									"number": {
										Type:     cty.Number,
										Computed: true,
									},
								},
							},
						},
					},
				},
				PlanResourceChangeFn: func(request providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
					return providers.PlanResourceChangeResponse{
						PlannedState: cty.ObjectVal(map[string]cty.Value{
							"id": cty.UnknownVal(cty.String),
						}),
					}
				},
				ApplyResourceChangeFn: func(request providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
					return providers.ApplyResourceChangeResponse{
						NewState: cty.ObjectVal(map[string]cty.Value{
							"id": cty.StringVal("7A9F887D-44C7-4281-80E5-578E41F99DFC"),
						}),
					}
				},
				ReadDataSourceFn: func(request providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
					values := request.Config.AsValueMap()
					if id, ok := values["id"]; ok {
						if id.IsKnown() && id.AsString() == "7A9F887D-44C7-4281-80E5-578E41F99DFC" {
							return providers.ReadDataSourceResponse{
								State: cty.ObjectVal(map[string]cty.Value{
									"id":     cty.StringVal("7A9F887D-44C7-4281-80E5-578E41F99DFC"),
									"number": cty.NumberIntVal(0),
								}),
							}
						}
					}

					return providers.ReadDataSourceResponse{
						Diagnostics: tfdiags.Diagnostics{tfdiags.Sourceless(tfdiags.Error, "shouldn't make it here", "really shouldn't make it here")},
					}
				},
			},
		},
		"failing nested data source doesn't block the plan": {
			configs: map[string]string{
				"main.tf": `
provider "checks" {}

check "error" {
  data "checks_object" "data_block" {}

  assert {
    condition = data.checks_object.data_block.number >= 0
    error_message = "negative number"
  }
}
`,
			},
			plan: map[string]checksTestingStatus{
				"error": {
					status: checks.StatusFail,
					messages: []string{
						"data source read failed: something bad happened and the provider couldn't read the data source",
					},
					refs: [][]string{
						{
							"data.checks_object.data_block",
						},
					},
				},
			},
			apply: map[string]checksTestingStatus{
				"error": {
					status: checks.StatusFail,
					messages: []string{
						"data source read failed: something bad happened and the provider couldn't read the data source",
					},
					refs: [][]string{
						{
							"data.checks_object.data_block",
						},
					},
				},
			},
			provider: &MockProvider{
				Meta: "checks",
				GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
					DataSources: map[string]providers.Schema{
						"checks_object": {
							Block: &configschema.Block{
								Attributes: map[string]*configschema.Attribute{
									"number": {
										Type:     cty.Number,
										Computed: true,
									},
								},
							},
						},
					},
				},
				ReadDataSourceFn: func(request providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
					return providers.ReadDataSourceResponse{
						Diagnostics: tfdiags.Diagnostics{tfdiags.Sourceless(tfdiags.Error, "data source read failed", "something bad happened and the provider couldn't read the data source")},
					}
				},
			},
		},
		"check failing in state and passing after plan and apply": {
			configs: map[string]string{
				"main.tf": `
provider "checks" {}

resource "checks_object" "resource" {
  number = 0
}

check "passing" {
  assert {
    condition     = checks_object.resource.number >= 0
    error_message = "negative number"
  }
}
`,
			},
			state: states.BuildState(func(state *states.SyncState) {
				state.SetResourceInstanceCurrent(
					addrs.Resource{
						Mode: addrs.ManagedResourceMode,
						Type: "checks_object",
						Name: "resource",
					}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
					&states.ResourceInstanceObjectSrc{
						Status:    states.ObjectReady,
						AttrsJSON: []byte(`{"number": -1}`),
					},
					addrs.AbsProviderConfig{
						Provider: addrs.NewDefaultProvider("test"),
						Module:   addrs.RootModule,
					})
			}),
			plan: map[string]checksTestingStatus{
				"passing": {
					status: checks.StatusPass,
				},
			},
			apply: map[string]checksTestingStatus{
				"passing": {
					status: checks.StatusPass,
				},
			},
			provider: &MockProvider{
				Meta: "checks",
				GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
					ResourceTypes: map[string]providers.Schema{
						"checks_object": {
							Block: &configschema.Block{
								Attributes: map[string]*configschema.Attribute{
									"number": {
										Type:     cty.Number,
										Required: true,
									},
								},
							},
						},
					},
				},
				PlanResourceChangeFn: func(request providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
					return providers.PlanResourceChangeResponse{
						PlannedState: cty.ObjectVal(map[string]cty.Value{
							"number": cty.NumberIntVal(0),
						}),
					}
				},
				ApplyResourceChangeFn: func(request providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
					return providers.ApplyResourceChangeResponse{
						NewState: cty.ObjectVal(map[string]cty.Value{
							"number": cty.NumberIntVal(0),
						}),
					}
				},
			},
		},
		"failing data source does block the plan": {
			configs: map[string]string{
				"main.tf": `
provider "checks" {}

data "checks_object" "data_block" {}

check "error" {
  assert {
    condition = data.checks_object.data_block.number >= 0
    error_message = "negative number"
  }
}
`,
			},
			planError: "data source read failed: something bad happened and the provider couldn't read the data source",
			provider: &MockProvider{
				Meta: "checks",
				GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
					DataSources: map[string]providers.Schema{
						"checks_object": {
							Block: &configschema.Block{
								Attributes: map[string]*configschema.Attribute{
									"number": {
										Type:     cty.Number,
										Computed: true,
									},
								},
							},
						},
					},
				},
				ReadDataSourceFn: func(request providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
					return providers.ReadDataSourceResponse{
						Diagnostics: tfdiags.Diagnostics{tfdiags.Sourceless(tfdiags.Error, "data source read failed", "something bad happened and the provider couldn't read the data source")},
					}
				},
			},
		},
		"invalid reference into check block": {
			configs: map[string]string{
				"main.tf": `
provider "checks" {}

data "checks_object" "data_block" {
  id = data.checks_object.nested_data_block.id
}

check "error" {
  data "checks_object" "nested_data_block" {}

  assert {
    condition = data.checks_object.data_block.number >= 0
    error_message = "negative number"
  }
}
`,
			},
			planError: "Reference to scoped resource: The referenced data resource \"checks_object\" \"nested_data_block\" is not available from this context.",
			provider: &MockProvider{
				Meta: "checks",
				GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
					DataSources: map[string]providers.Schema{
						"checks_object": {
							Block: &configschema.Block{
								Attributes: map[string]*configschema.Attribute{
									"id": {
										Type:     cty.String,
										Computed: true,
										Optional: true,
									},
								},
							},
						},
					},
				},
				ReadDataSourceFn: func(request providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
					input := request.Config.AsValueMap()
					if _, ok := input["id"]; ok {
						return providers.ReadDataSourceResponse{
							State: request.Config,
						}
					}

					return providers.ReadDataSourceResponse{
						State: cty.ObjectVal(map[string]cty.Value{
							"id": cty.UnknownVal(cty.String),
						}),
					}
				},
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			configs := testModuleInline(t, test.configs)
			ctx := testContext2(t, &ContextOpts{
				Providers: map[addrs.Provider]providers.Factory{
					addrs.NewDefaultProvider(test.provider.Meta.(string)): testProviderFuncFixed(test.provider),
				},
			})

			initialState := states.NewState()
			if test.state != nil {
				initialState = test.state
			}

			plan, diags := ctx.Plan(configs, initialState, &PlanOpts{
				Mode: plans.NormalMode,
			})
			if validateError(t, "planning", test.planError, diags) {
				return
			}
			validateCheckResults(t, "planning", test.plan, plan.Checks)

			if test.providerHook != nil {
				// This gives an opportunity to change the behaviour of the
				// provider between the plan and apply stages.
				test.providerHook(test.provider)
			}

			state, diags := ctx.Apply(plan, configs)
			if validateError(t, "apply", test.applyError, diags) {
				return
			}
			validateCheckResults(t, "apply", test.apply, state.CheckResults)
		})
	}
}

func validateError(t *testing.T, stage string, expected string, actual tfdiags.Diagnostics) bool {
	if expected != "" {
		if !actual.HasErrors() {
			t.Errorf("expected %s to error with \"%s\", but no errors were returned", stage, expected)
		} else if expected != actual.Err().Error() {
			t.Errorf("expected %s to error with \"%s\" but found \"%s\"", stage, expected, actual.Err())
		}
		return true
	}

	assertNoErrors(t, actual)
	return false
}

func validateCheckResults(t *testing.T, stage string, expected map[string]checksTestingStatus, actual *states.CheckResults) {

	// Just a quick sanity check that the plan or apply process didn't create
	// some non-existent checks.
	if len(expected) != len(actual.ConfigResults.Keys()) {
		t.Errorf("expected %d check results but found %d after %s", len(expected), len(actual.ConfigResults.Keys()), stage)
	}

	// Now, lets make sure the checks all match what we expect.
	for check, want := range expected {
		results := actual.GetObjectResult(addrs.Check{
			Name: check,
		}.Absolute(addrs.RootModuleInstance))

		if results.Status != want.status {
			t.Errorf("%s: wanted %s but got %s after %s", check, want.status, results.Status, stage)
		}

		if len(want.messages) != len(results.FailureMessages) {
			t.Errorf("%s: expected %d failure messages but had %d after %s", check, len(want.messages), len(results.FailureMessages), stage)
		}

		if len(results.FailureMessages) != len(results.Refs) {
			t.Errorf("%s: returned a different number of references compared to failure messages", check)
		}

		validateLists(t, want.messages, results.FailureMessages, func(t *testing.T, ix int, expected, actual string) {
			if actual != expected {
				t.Errorf("%s: expected failure message at %d to be \"%s\" but was \"%s\" after %s", check, ix, expected, actual, stage)
			}
		})

		validateLists(t, want.refs, results.Refs, func(t *testing.T, ix int, expected, actual []string) {
			validateLists(t, expected, actual, func(t *testing.T, jx int, expected, actual string) {
				if actual != expected {
					t.Errorf("%s: expected reference at (%d,%d) to be \"%s\" but was \"%s\" after %s", check, ix, jx, expected, actual, stage)
				}
			})
		})
	}
}

func validateLists[Element any](t *testing.T, expected, actual []Element, validate func(t *testing.T, ix int, left, right Element)) {
	max := len(expected)
	if len(actual) > max {
		max = len(actual)
	}

	for ix := 0; ix < max; ix++ {
		var e, a Element
		if ix < len(expected) {
			e = expected[ix]
		}
		if ix < len(actual) {
			a = actual[ix]
		}

		validate(t, ix, e, a)
	}
}
