// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: BUSL-1.1

package stackplan

import (
	"fmt"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/zclconf/go-cty/cty"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/hashicorp/terraform/internal/addrs"
	"github.com/hashicorp/terraform/internal/collections"
	"github.com/hashicorp/terraform/internal/configs/configschema"
	"github.com/hashicorp/terraform/internal/plans"
	"github.com/hashicorp/terraform/internal/plans/planfile"
	"github.com/hashicorp/terraform/internal/plans/planproto"
	"github.com/hashicorp/terraform/internal/providers"
	"github.com/hashicorp/terraform/internal/rpcapi/terraform1"
	"github.com/hashicorp/terraform/internal/stacks/stackaddrs"
	"github.com/hashicorp/terraform/internal/stacks/stackutils"
	"github.com/hashicorp/terraform/internal/stacks/tfstackdata1"
	"github.com/hashicorp/terraform/internal/states"
)

// PlannedChange represents a single isolated planned changed, emitted as
// part of a stream of planned changes during the PlanStackChanges RPC API
// operation.
//
// Each PlannedChange becomes a single event in the RPC API, which itself
// has zero or more opaque raw plan messages that the caller must collect and
// provide verbatim during planning and zero or one "description" messages
// that are to give the caller realtime updates about the planning process.
//
// The aggregated sequence of "raw" messages can be provided later to
// [LoadFromProto] to obtain a [Plan] object containing the information
// Terraform Core would need to apply the plan.
type PlannedChange interface {
	// PlannedChangeProto returns the protocol buffers representation of
	// the change, ready to be sent verbatim to an RPC API client.
	PlannedChangeProto() (*terraform1.PlannedChange, error)
}

// PlannedChangeRootInputValue announces the existence of a root stack input
// variable and captures its plan-time value so we can make sure to use
// the same value during the apply phase.
type PlannedChangeRootInputValue struct {
	Addr stackaddrs.InputVariable

	// Value is the value we used for the variable during planning, or
	// [cty.NilVal] if the variable was declared as ephemeral and therefore
	// its value must not be persisted between phases.
	Value cty.Value

	// RequiredOnApply is true if a non-null value for this variable
	// must be supplied during the apply phase.
	//
	// If this field is false then the variable must either be left unset
	// or must be set to the same value during the apply phase, both of
	// which are equivalent.
	//
	// This is set for an input variable that was declared as ephemeral
	// and was set to a non-null value during the planning phase. The
	// "null-ness" of an ephemeral value is not allowed to change between
	// plan and apply, but a value set during planning can have a different
	// value during apply.
	RequiredOnApply bool
}

var _ PlannedChange = (*PlannedChangeRootInputValue)(nil)

// PlannedChangeProto implements PlannedChange.
func (pc *PlannedChangeRootInputValue) PlannedChangeProto() (*terraform1.PlannedChange, error) {
	// We use cty.DynamicPseudoType here so that we'll save both the
	// value _and_ its dynamic type in the plan, so we can recover
	// exactly the same value later.
	var ppdv *planproto.DynamicValue
	if pc.Value != cty.NilVal {
		dv, err := plans.NewDynamicValue(pc.Value, cty.DynamicPseudoType)
		if err != nil {
			return nil, fmt.Errorf("can't encode value for %s: %w", pc.Addr, err)
		}
		ppdv = &planproto.DynamicValue{
			Msgpack: dv,
		}
	}

	var raw anypb.Any
	err := anypb.MarshalFrom(&raw, &tfstackdata1.PlanRootInputValue{
		Name:            pc.Addr.Name,
		Value:           ppdv,
		RequiredOnApply: pc.RequiredOnApply,
	}, proto.MarshalOptions{})
	if err != nil {
		return nil, err
	}

	var descs []*terraform1.PlannedChange_ChangeDescription
	if pc.RequiredOnApply {
		// We only include a change description for the subset of variables
		// which must be re-supplied during apply. This allows an apply-time
		// caller to know which subset of variables it needs to provide.
		descs = []*terraform1.PlannedChange_ChangeDescription{
			{
				Description: &terraform1.PlannedChange_ChangeDescription_ApplyTimeInputVariable{
					ApplyTimeInputVariable: &terraform1.PlannedChange_InputVariableDuringApply{
						Name: pc.Addr.Name,
					},
				},
			},
		}
	}

	return &terraform1.PlannedChange{
		Raw:          []*anypb.Any{&raw},
		Descriptions: descs,
	}, nil
}

// PlannedChangeComponentInstance announces the existence of a component
// instance and describes (using a plan action) whether it is being added
// or removed.
type PlannedChangeComponentInstance struct {
	Addr stackaddrs.AbsComponentInstance

	// PlanApplyable is true if the modules runtime ruled that this particular
	// component's plan is applyable.
	//
	// See the documentation for [plans.Plan.Applyable] for details on what
	// exactly this represents.
	PlanApplyable bool

	// PlanApplyable is true if the modules runtime ruled that this particular
	// component's plan is complete.
	//
	// See the documentation for [plans.Plan.Complete] for details on what
	// exactly this represents.
	PlanComplete bool

	// Action describes any difference in the existence of this component
	// instance compared to the prior state.
	//
	// Currently it can only be "Create", "Delete", or "NoOp". This action
	// relates to the existence of the component instance itself and does
	// not consider the resource instances inside, whose change actions
	// are tracked in their own [PlannedChange] objects.
	Action plans.Action

	// Mode describes the mode that the component instance is being planned
	// in.
	Mode plans.Mode

	// RequiredComponents is a set of the addresses of all of the components
	// that provide infrastructure that this one's infrastructure will
	// depend on. Any component named here must exist for the entire lifespan
	// of this component instance.
	RequiredComponents collections.Set[stackaddrs.AbsComponent]

	// PlannedInputValues records our best approximation of the component's
	// topmost input values during the planning phase. This could contain
	// unknown values if one component is configured from results of another.
	// This therefore won't be used directly as the input values during apply,
	// but the final set of input values during apply should be consistent
	// with what's captured here.
	PlannedInputValues map[string]plans.DynamicValue

	PlannedInputValueMarks map[string][]cty.PathValueMarks

	PlannedOutputValues map[string]cty.Value

	PlannedCheckResults *states.CheckResults

	// PlanTimestamp is the timestamp that would be returned from the
	// "plantimestamp" function in modules inside this component. We
	// must preserve this in the raw plan data to ensure that we can
	// return the same timestamp again during the apply phase.
	PlanTimestamp time.Time
}

var _ PlannedChange = (*PlannedChangeComponentInstance)(nil)

// PlannedChangeProto implements PlannedChange.
func (pc *PlannedChangeComponentInstance) PlannedChangeProto() (*terraform1.PlannedChange, error) {
	var plannedInputValues map[string]*tfstackdata1.DynamicValue
	if n := len(pc.PlannedInputValues); n != 0 {
		plannedInputValues = make(map[string]*tfstackdata1.DynamicValue, n)
		for k, v := range pc.PlannedInputValues {
			var sensitivePaths []*planproto.Path
			if pvm, ok := pc.PlannedInputValueMarks[k]; ok {
				for _, p := range pvm {
					path, err := planproto.NewPath(p.Path)
					if err != nil {
						return nil, err
					}
					sensitivePaths = append(sensitivePaths, path)
				}
			}
			plannedInputValues[k] = &tfstackdata1.DynamicValue{
				Value: &planproto.DynamicValue{
					Msgpack: v,
				},
				SensitivePaths: sensitivePaths,
			}
		}
	}

	var planTimestampStr string
	var zeroTime time.Time
	if pc.PlanTimestamp != zeroTime {
		planTimestampStr = pc.PlanTimestamp.Format(time.RFC3339)
	}

	componentAddrsRaw := make([]string, 0, pc.RequiredComponents.Len())
	for _, componentAddr := range pc.RequiredComponents.Elems() {
		componentAddrsRaw = append(componentAddrsRaw, componentAddr.String())
	}

	plannedOutputValues := make(map[string]*tfstackdata1.DynamicValue)
	for k, v := range pc.PlannedOutputValues {
		dv, err := tfstackdata1.DynamicValueToTFStackData1(v, cty.DynamicPseudoType)
		if err != nil {
			return nil, fmt.Errorf("encoding output value %q: %w", k, err)
		}
		plannedOutputValues[k] = dv
	}

	plannedCheckResults, err := planfile.CheckResultsToPlanProto(pc.PlannedCheckResults)
	if err != nil {
		return nil, fmt.Errorf("failed to encode check results: %s", err)
	}

	mode, err := planproto.NewMode(pc.Mode)
	if err != nil {
		return nil, fmt.Errorf("failed to encode mode: %s", err)
	}

	var raw anypb.Any
	err = anypb.MarshalFrom(&raw, &tfstackdata1.PlanComponentInstance{
		ComponentInstanceAddr:   pc.Addr.String(),
		PlanTimestamp:           planTimestampStr,
		PlannedInputValues:      plannedInputValues,
		PlannedAction:           planproto.NewAction(pc.Action),
		Mode:                    mode,
		PlanApplyable:           pc.PlanApplyable,
		PlanComplete:            pc.PlanComplete,
		DependsOnComponentAddrs: componentAddrsRaw,
		PlannedOutputValues:     plannedOutputValues,
		PlannedCheckResults:     plannedCheckResults,
	}, proto.MarshalOptions{})
	if err != nil {
		return nil, err
	}

	protoChangeTypes, err := terraform1.ChangeTypesForPlanAction(pc.Action)
	if err != nil {
		return nil, err
	}

	return &terraform1.PlannedChange{
		Raw: []*anypb.Any{&raw},
		Descriptions: []*terraform1.PlannedChange_ChangeDescription{
			{
				Description: &terraform1.PlannedChange_ChangeDescription_ComponentInstancePlanned{
					ComponentInstancePlanned: &terraform1.PlannedChange_ComponentInstance{
						Addr: &terraform1.ComponentInstanceInStackAddr{
							ComponentAddr:         stackaddrs.ConfigComponentForAbsInstance(pc.Addr).String(),
							ComponentInstanceAddr: pc.Addr.String(),
						},
						Actions:      protoChangeTypes,
						PlanComplete: pc.PlanComplete,
						// We don't include "applyable" in here since for a
						// stack operation it's the overall stack plan applyable
						// flag that matters, and the per-component flags
						// are just an implementation detail.
					},
				},
			},
		},
	}, nil
}

// PlannedChangeResourceInstancePlanned announces an action that Terraform
// is proposing to take if this plan is applied.
type PlannedChangeResourceInstancePlanned struct {
	ResourceInstanceObjectAddr stackaddrs.AbsResourceInstanceObject

	// ChangeSrc describes the planned change, if any. This can be nil if
	// we're only intending to update the state to match PriorStateSrc.
	ChangeSrc *plans.ResourceInstanceChangeSrc

	// PriorStateSrc describes the "prior state" that the planned change, if
	// any, was generated against.
	//
	// This can be nil if the object didn't previously exist. If both
	// PriorStateSrc and ChangeSrc are nil then that suggests that the
	// object existed in the previous run's state but was found to no
	// longer exist while refreshing during plan.
	PriorStateSrc *states.ResourceInstanceObjectSrc

	// ProviderConfigAddr is the address of the provider configuration
	// that planned this change, resolved in terms of the configuration for
	// the component this resource instance object belongs to.
	ProviderConfigAddr addrs.AbsProviderConfig

	// Schema MUST be the same schema that was used to encode the dynamic
	// values inside ChangeSrc and PriorStateSrc.
	//
	// Can be nil if and only if ChangeSrc and PriorStateSrc are both nil
	// themselves.
	Schema *configschema.Block
}

var _ PlannedChange = (*PlannedChangeResourceInstancePlanned)(nil)

func (pc *PlannedChangeResourceInstancePlanned) PlanResourceInstanceChangePlannedProto() (*tfstackdata1.PlanResourceInstanceChangePlanned, error) {
	rioAddr := pc.ResourceInstanceObjectAddr

	if pc.ChangeSrc == nil && pc.PriorStateSrc == nil {
		// This is just a stubby placeholder to remind us to drop the
		// apparently-deleted-outside-of-Terraform object from the state
		// if this plan later gets applied.

		return &tfstackdata1.PlanResourceInstanceChangePlanned{
			ComponentInstanceAddr: rioAddr.Component.String(),
			ResourceInstanceAddr:  rioAddr.Item.ResourceInstance.String(),
			DeposedKey:            rioAddr.Item.DeposedKey.String(),
			ProviderConfigAddr:    pc.ProviderConfigAddr.String(),
		}, nil
	}

	// We include the prior state as part of the raw plan because that
	// contains the result of upgrading the state to the provider's latest
	// schema version and incorporating any changes detected in the refresh
	// step, which we'll rely on during the apply step to make sure that
	// the final plan is consistent, etc.
	priorStateProto := tfstackdata1.ResourceInstanceObjectStateToTFStackData1(pc.PriorStateSrc, pc.ProviderConfigAddr)

	changeProto, err := planfile.ResourceChangeToProto(pc.ChangeSrc)
	if err != nil {
		return nil, fmt.Errorf("converting resource instance change to proto: %w", err)
	}

	return &tfstackdata1.PlanResourceInstanceChangePlanned{
		ComponentInstanceAddr: rioAddr.Component.String(),
		ResourceInstanceAddr:  rioAddr.Item.ResourceInstance.String(),
		DeposedKey:            rioAddr.Item.DeposedKey.String(),
		ProviderConfigAddr:    pc.ProviderConfigAddr.String(),
		Change:                changeProto,
		PriorState:            priorStateProto,
	}, nil
}

func (pc *PlannedChangeResourceInstancePlanned) ChangeDescription() (*terraform1.PlannedChange_ChangeDescription, error) {
	rioAddr := pc.ResourceInstanceObjectAddr
	// We only emit an external description if there's a change to describe.
	// Otherwise, we just emit a raw to remind us to update the state for
	// this object during the apply step, to match the prior state.
	if pc.ChangeSrc == nil {
		return nil, nil
	}

	protoChangeTypes, err := terraform1.ChangeTypesForPlanAction(pc.ChangeSrc.Action)
	if err != nil {
		return nil, err
	}
	replacePaths, err := encodePathSet(pc.ChangeSrc.RequiredReplace)
	if err != nil {
		return nil, err
	}

	var moved *terraform1.PlannedChange_ResourceInstance_Moved
	var imported *terraform1.PlannedChange_ResourceInstance_Imported

	if pc.ChangeSrc.Moved() {
		moved = &terraform1.PlannedChange_ResourceInstance_Moved{
			PrevAddr: terraform1.NewResourceInstanceInStackAddr(stackaddrs.AbsResourceInstance{
				Component: rioAddr.Component,
				Item:      pc.ChangeSrc.PrevRunAddr,
			}),
		}
	}

	if pc.ChangeSrc.Importing != nil {
		imported = &terraform1.PlannedChange_ResourceInstance_Imported{
			ImportId: pc.ChangeSrc.Importing.ID,
			Unknown:  pc.ChangeSrc.Importing.Unknown,
		}
	}

	return &terraform1.PlannedChange_ChangeDescription{
		Description: &terraform1.PlannedChange_ChangeDescription_ResourceInstancePlanned{
			ResourceInstancePlanned: &terraform1.PlannedChange_ResourceInstance{
				Addr:         terraform1.NewResourceInstanceObjectInStackAddr(rioAddr),
				ResourceMode: stackutils.ResourceModeForProto(pc.ChangeSrc.Addr.Resource.Resource.Mode),
				ResourceType: pc.ChangeSrc.Addr.Resource.Resource.Type,
				ProviderAddr: pc.ChangeSrc.ProviderAddr.Provider.String(),

				Actions: protoChangeTypes,
				Values: &terraform1.DynamicValueChange{
					Old: terraform1.NewDynamicValue(
						pc.ChangeSrc.Before,
						pc.ChangeSrc.BeforeSensitivePaths,
					),
					New: terraform1.NewDynamicValue(
						pc.ChangeSrc.After,
						pc.ChangeSrc.AfterSensitivePaths,
					),
				},
				ReplacePaths: replacePaths,
				Moved:        moved,
				Imported:     imported,
			},
		},
	}, nil

}

// PlannedChangeProto implements PlannedChange.
func (pc *PlannedChangeResourceInstancePlanned) PlannedChangeProto() (*terraform1.PlannedChange, error) {
	pric, err := pc.PlanResourceInstanceChangePlannedProto()
	if err != nil {
		return nil, err
	}
	var raw anypb.Any
	err = anypb.MarshalFrom(&raw, pric, proto.MarshalOptions{})
	if err != nil {
		return nil, err
	}

	if pc.ChangeSrc == nil && pc.PriorStateSrc == nil {
		// We only emit a "raw" in this case, because this is a relatively
		// uninteresting edge-case. The PlanResourceInstanceChangePlannedProto
		// function should have returned a placeholder value for this use case.

		return &terraform1.PlannedChange{
			Raw: []*anypb.Any{&raw},
		}, nil
	}

	var descs []*terraform1.PlannedChange_ChangeDescription
	desc, err := pc.ChangeDescription()
	if err != nil {
		return nil, err
	}
	if desc != nil {
		descs = append(descs, desc)
	}

	return &terraform1.PlannedChange{
		Raw:          []*anypb.Any{&raw},
		Descriptions: descs,
	}, nil
}

// PlannedChangeDeferredResourceInstancePlanned announces that an action that Terraform
// is proposing to take if this plan is applied is being deferred.
type PlannedChangeDeferredResourceInstancePlanned struct {
	// ResourceInstancePlanned is the planned change that is being deferred.
	ResourceInstancePlanned PlannedChangeResourceInstancePlanned

	// DeferredReason is the reason why the change is being deferred.
	DeferredReason providers.DeferredReason
}

var _ PlannedChange = (*PlannedChangeDeferredResourceInstancePlanned)(nil)

// PlannedChangeProto implements PlannedChange.
func (dpc *PlannedChangeDeferredResourceInstancePlanned) PlannedChangeProto() (*terraform1.PlannedChange, error) {
	change, err := dpc.ResourceInstancePlanned.PlanResourceInstanceChangePlannedProto()
	if err != nil {
		return nil, err
	}

	// We'll ignore the error here. We certainly should not have got this far
	// if we have a deferred reason that the Terraform Core runtime doesn't
	// recognise. There will be diagnostics elsewhere to reflect this, as we
	// can just use INVALID to capture this. This also makes us forwards and
	// backwards compatible, as we'll return INVALID for any new deferred
	// reasons that are added in the future without erroring.
	deferredReason, _ := planfile.DeferredReasonToProto(dpc.DeferredReason)

	var raw anypb.Any
	err = anypb.MarshalFrom(&raw, &tfstackdata1.PlanDeferredResourceInstanceChange{
		Change: change,
		Deferred: &planproto.Deferred{
			Reason: deferredReason,
		},
	}, proto.MarshalOptions{})
	if err != nil {
		return nil, err
	}
	ricd, err := dpc.ResourceInstancePlanned.ChangeDescription()
	if err != nil {
		return nil, err
	}

	var descs []*terraform1.PlannedChange_ChangeDescription
	descs = append(descs, &terraform1.PlannedChange_ChangeDescription{
		Description: &terraform1.PlannedChange_ChangeDescription_ResourceInstanceDeferred{
			ResourceInstanceDeferred: &terraform1.PlannedChange_ResourceInstanceDeferred{
				ResourceInstance: ricd.GetResourceInstancePlanned(),
				Deferred:         EncodeDeferred(dpc.DeferredReason),
			},
		},
	})

	return &terraform1.PlannedChange{
		Raw:          []*anypb.Any{&raw},
		Descriptions: descs,
	}, nil
}

func EncodeDeferred(reason providers.DeferredReason) *terraform1.Deferred {
	deferred := new(terraform1.Deferred)
	switch reason {
	case providers.DeferredReasonInstanceCountUnknown:
		deferred.Reason = terraform1.Deferred_INSTANCE_COUNT_UNKNOWN
	case providers.DeferredReasonResourceConfigUnknown:
		deferred.Reason = terraform1.Deferred_RESOURCE_CONFIG_UNKNOWN
	case providers.DeferredReasonProviderConfigUnknown:
		deferred.Reason = terraform1.Deferred_PROVIDER_CONFIG_UNKNOWN
	case providers.DeferredReasonAbsentPrereq:
		deferred.Reason = terraform1.Deferred_ABSENT_PREREQ
	case providers.DeferredReasonDeferredPrereq:
		deferred.Reason = terraform1.Deferred_DEFERRED_PREREQ
	default:
		deferred.Reason = terraform1.Deferred_INVALID
	}
	return deferred
}

func encodePathSet(pathSet cty.PathSet) ([]*terraform1.AttributePath, error) {
	if pathSet.Empty() {
		return nil, nil
	}

	pathList := pathSet.List()
	paths := make([]*terraform1.AttributePath, 0, len(pathList))

	for _, path := range pathList {
		paths = append(paths, terraform1.NewAttributePath(path))
	}
	return paths, nil
}

// PlannedChangeOutputValue announces the change action for one output value
// declared in the top-level stack configuration.
//
// This change type only includes an external description, and does not
// contribute anything to the raw plan sequence.
type PlannedChangeOutputValue struct {
	Addr   stackaddrs.OutputValue // Covers only root stack output values
	Action plans.Action

	OldValue, NewValue                             plans.DynamicValue
	OldValueSensitivePaths, NewValueSensitivePaths []cty.Path
}

var _ PlannedChange = (*PlannedChangeOutputValue)(nil)

// PlannedChangeProto implements PlannedChange.
func (pc *PlannedChangeOutputValue) PlannedChangeProto() (*terraform1.PlannedChange, error) {
	protoChangeTypes, err := terraform1.ChangeTypesForPlanAction(pc.Action)
	if err != nil {
		return nil, err
	}

	return &terraform1.PlannedChange{
		// No "raw" representation for output values; we emit them only for
		// external consumption, since Terraform Core will just recalculate
		// them during apply anyway.
		Descriptions: []*terraform1.PlannedChange_ChangeDescription{
			{
				Description: &terraform1.PlannedChange_ChangeDescription_OutputValuePlanned{
					OutputValuePlanned: &terraform1.PlannedChange_OutputValue{
						Name:    pc.Addr.Name,
						Actions: protoChangeTypes,

						Values: &terraform1.DynamicValueChange{
							Old: terraform1.NewDynamicValue(pc.OldValue, pc.OldValueSensitivePaths),
							New: terraform1.NewDynamicValue(pc.NewValue, pc.NewValueSensitivePaths),
						},
					},
				},
			},
		},
	}, nil
}

// PlannedChangeHeader is a special change type we typically emit before any
// others to capture overall metadata about a plan. [LoadFromProto] fails if
// asked to decode a plan sequence that doesn't include at least one raw
// message generated from this change type.
//
// PlannedChangeHeader has only a raw message and does not contribute to
// the external-facing plan description.
type PlannedChangeHeader struct {
	TerraformVersion *version.Version
}

var _ PlannedChange = (*PlannedChangeHeader)(nil)

// PlannedChangeProto implements PlannedChange.
func (pc *PlannedChangeHeader) PlannedChangeProto() (*terraform1.PlannedChange, error) {
	var raw anypb.Any
	err := anypb.MarshalFrom(&raw, &tfstackdata1.PlanHeader{
		TerraformVersion: pc.TerraformVersion.String(),
	}, proto.MarshalOptions{})
	if err != nil {
		return nil, err
	}

	return &terraform1.PlannedChange{
		Raw: []*anypb.Any{&raw},
	}, nil
}

// PlannedChangePriorStateElement is a special change type we emit to capture
// each element of the prior state.
//
// PlannedChangePriorStateElement has only a raw message and does not
// contribute to the external-facing plan description, since it's really just
// an implementation detail that allows us to deal with various state cleanup
// concerns during the apply phase; this isn't really a "planned change" in
// the typical sense.
type PlannedChangePriorStateElement struct {
	Key string
	Raw *anypb.Any
}

var _ PlannedChange = (*PlannedChangePriorStateElement)(nil)

// PlannedChangeProto implements PlannedChange.
func (pc *PlannedChangePriorStateElement) PlannedChangeProto() (*terraform1.PlannedChange, error) {
	var raw anypb.Any
	err := anypb.MarshalFrom(&raw, &tfstackdata1.PlanPriorStateElem{
		Key: pc.Key,
		Raw: pc.Raw,
	}, proto.MarshalOptions{})
	if err != nil {
		return nil, err
	}

	return &terraform1.PlannedChange{
		Raw: []*anypb.Any{&raw},
	}, nil
}

// PlannedChangePlannedTimestamp is a special change type we emit to record the timestamp
// of when the plan was generated. This is being used in the plantimestamp function.
type PlannedChangePlannedTimestamp struct {
	PlannedTimestamp time.Time
}

var _ PlannedChange = (*PlannedChangePlannedTimestamp)(nil)

// PlannedChangeProto implements PlannedChange.
func (pc *PlannedChangePlannedTimestamp) PlannedChangeProto() (*terraform1.PlannedChange, error) {
	var raw anypb.Any
	err := anypb.MarshalFrom(&raw, &tfstackdata1.PlanTimestamp{
		PlanTimestamp: pc.PlannedTimestamp.Format(time.RFC3339),
	}, proto.MarshalOptions{})
	if err != nil {
		return nil, err
	}

	return &terraform1.PlannedChange{
		Raw: []*anypb.Any{&raw},
	}, nil
}

// PlannedChangeApplyable is a special change type we typically append at the
// end of the raw plan stream to represent that the planning process ran to
// completion without encountering any errors, and therefore the plan could
// potentially be applied.
type PlannedChangeApplyable struct {
	Applyable bool
}

var _ PlannedChange = (*PlannedChangeApplyable)(nil)

// PlannedChangeProto implements PlannedChange.
func (pc *PlannedChangeApplyable) PlannedChangeProto() (*terraform1.PlannedChange, error) {
	var raw anypb.Any
	err := anypb.MarshalFrom(&raw, &tfstackdata1.PlanApplyable{
		Applyable: pc.Applyable,
	}, proto.MarshalOptions{})
	if err != nil {
		return nil, err
	}

	return &terraform1.PlannedChange{
		Raw: []*anypb.Any{&raw},
		Descriptions: []*terraform1.PlannedChange_ChangeDescription{
			{
				Description: &terraform1.PlannedChange_ChangeDescription_PlanApplyable{
					PlanApplyable: pc.Applyable,
				},
			},
		},
	}, nil
}
