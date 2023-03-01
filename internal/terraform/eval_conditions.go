package terraform

import (
	"fmt"
	"log"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/hashicorp/terraform/internal/addrs"
	"github.com/hashicorp/terraform/internal/checks"
	"github.com/hashicorp/terraform/internal/configs"
	"github.com/hashicorp/terraform/internal/instances"
	"github.com/hashicorp/terraform/internal/lang"
	"github.com/hashicorp/terraform/internal/lang/marks"
	"github.com/hashicorp/terraform/internal/tfdiags"
)

// evalCheckRules ensures that all of the given check rules pass against
// the given HCL evaluation context.
//
// If any check rules produce an unknown result then they will be silently
// ignored on the assumption that the same checks will be run again later
// with fewer unknown values in the EvalContext.
//
// If any of the rules do not pass, the returned diagnostics will contain
// errors. Otherwise, it will either be empty or contain only warnings.
func evalCheckRules(typ addrs.CheckRuleType, rules []*configs.CheckRule, ctx EvalContext, self addrs.Checkable, keyData instances.RepetitionData, diagSeverity tfdiags.Severity) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	checkState := ctx.Checks()
	if !checkState.ConfigHasChecks(self.ConfigCheckable()) {
		// We have nothing to do if this object doesn't have any checks,
		// but the "rules" slice should agree that we don't.
		if ct := len(rules); ct != 0 {
			panic(fmt.Sprintf("check state says that %s should have no rules, but it has %d", self, ct))
		}
		return diags
	}

	if len(rules) == 0 {
		// Nothing to do
		return nil
	}

	severity := diagSeverity.ToHCL()

	for i, rule := range rules {
		result, ruleDiags := evalCheckRule(typ, rule, ctx, self, keyData, severity)
		diags = diags.Append(ruleDiags)

		log.Printf("[TRACE] evalCheckRules: %s status is now %s", self, result.Status)
		if result.Status == checks.StatusFail {
			var refs []addrs.Referenceable
			for _, ref := range result.Refs {
				refs = append(refs, ref.Subject)
			}
			checkState.ReportCheckFailure(self, typ, i, result.FailureMessage, refs)
		} else {
			checkState.ReportCheckResult(self, typ, i, result.Status)
		}
	}

	return diags
}

type checkResult struct {
	Status         checks.Status
	FailureMessage string
	Refs           []*addrs.Reference
}

type validateResult struct {
	Ctx          *hcl.EvalContext
	ErrorMessage string
	Refs         []*addrs.Reference
}

func validateCheckRule(typ addrs.CheckRuleType, rule *configs.CheckRule, ctx EvalContext, self addrs.Checkable, keyData instances.RepetitionData) (validateResult, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	refs, moreDiags := lang.ReferencesInExpr(rule.Condition)
	diags = diags.Append(moreDiags)
	moreRefs, moreDiags := lang.ReferencesInExpr(rule.ErrorMessage)
	diags = diags.Append(moreDiags)
	refs = append(refs, moreRefs...)

	var selfReference, sourceReference addrs.Referenceable
	switch typ {
	case addrs.ResourcePostcondition:
		switch s := self.(type) {
		case addrs.AbsResourceInstance:
			// Only resource postconditions can refer to self
			selfReference = s.Resource
		default:
			panic(fmt.Sprintf("Invalid self reference type %t", self))
		}
	case addrs.CheckAssertion:
		switch s := self.(type) {
		case addrs.AbsCheck:
			// Only check blocks have scoped resources so need to specify their
			// source.
			sourceReference = s.Check
		default:
			panic(fmt.Sprintf("Invalid source reference type %t", self))
		}
	}
	scope := ctx.EvaluationScope(selfReference, sourceReference, keyData)

	hclCtx, moreDiags := scope.EvalContext(refs)
	diags = diags.Append(moreDiags)

	errorMessage, moreDiags := evalCheckErrorMessage(rule.ErrorMessage, hclCtx)
	diags = diags.Append(moreDiags)

	return validateResult{
		Ctx:          hclCtx,
		ErrorMessage: errorMessage,
		Refs:         refs,
	}, diags
}

func evalCheckRule(typ addrs.CheckRuleType, rule *configs.CheckRule, ctx EvalContext, self addrs.Checkable, keyData instances.RepetitionData, severity hcl.DiagnosticSeverity) (checkResult, tfdiags.Diagnostics) {
	// NOTE: Intentionally not passing the caller's selected severity in here,
	// because this reports errors in the configuration itself, not the failure
	// of an otherwise-valid condition.
	validation, diags := validateCheckRule(typ, rule, ctx, self, keyData)

	const errInvalidCondition = "Invalid condition result"

	resultVal, hclDiags := rule.Condition.Value(validation.Ctx)
	diags = diags.Append(hclDiags)

	if diags.HasErrors() {
		log.Printf("[TRACE] evalCheckRule: %s: %s", typ, diags.Err().Error())
		return checkResult{Status: checks.StatusError}, diags
	}

	if !resultVal.IsKnown() {

		// Check assertions warn if a status is unknown.
		if typ == addrs.CheckAssertion {
			diags = diags.Append(&hcl.Diagnostic{
				Severity:    hcl.DiagWarning,
				Summary:     fmt.Sprintf("%s known after apply", typ.Description()),
				Detail:      "The condition could not be evaluated at this time, a result will be known when this plan is applied.",
				Subject:     rule.Condition.Range().Ptr(),
				Expression:  rule.Condition,
				EvalContext: validation.Ctx,
			})
		}

		// We'll wait until we've learned more, then.
		return checkResult{Status: checks.StatusUnknown}, diags
	}
	if resultVal.IsNull() {
		// NOTE: Intentionally not passing the caller's selected severity in here,
		// because this reports errors in the configuration itself, not the failure
		// of an otherwise-valid condition.
		diags = diags.Append(&hcl.Diagnostic{
			Severity:    hcl.DiagError,
			Summary:     errInvalidCondition,
			Detail:      "Condition expression must return either true or false, not null.",
			Subject:     rule.Condition.Range().Ptr(),
			Expression:  rule.Condition,
			EvalContext: validation.Ctx,
		})
		return checkResult{Status: checks.StatusError}, diags
	}
	var err error
	resultVal, err = convert.Convert(resultVal, cty.Bool)
	if err != nil {
		// NOTE: Intentionally not passing the caller's selected severity in here,
		// because this reports errors in the configuration itself, not the failure
		// of an otherwise-valid condition.
		detail := fmt.Sprintf("Invalid condition result value: %s.", tfdiags.FormatError(err))
		diags = diags.Append(&hcl.Diagnostic{
			Severity:    hcl.DiagError,
			Summary:     errInvalidCondition,
			Detail:      detail,
			Subject:     rule.Condition.Range().Ptr(),
			Expression:  rule.Condition,
			EvalContext: validation.Ctx,
		})
		return checkResult{Status: checks.StatusError}, diags
	}

	// The condition result may be marked if the expression refers to a
	// sensitive value.
	resultVal, _ = resultVal.Unmark()

	status := checks.StatusForCtyValue(resultVal)

	if status != checks.StatusFail {
		return checkResult{Status: status}, diags
	}

	errorMessageForDiags := validation.ErrorMessage
	if errorMessageForDiags == "" {
		errorMessageForDiags = "This check failed, but has an invalid error message as described in the other accompanying messages."
	}
	diags = diags.Append(&hcl.Diagnostic{
		// The caller gets to choose the severity of this one, because we
		// treat condition failures as warnings in the presence of
		// certain special planning options.
		Severity:    severity,
		Summary:     fmt.Sprintf("%s failed", typ.Description()),
		Detail:      errorMessageForDiags,
		Subject:     rule.Condition.Range().Ptr(),
		Expression:  rule.Condition,
		EvalContext: validation.Ctx,
	})

	return checkResult{
		Status:         status,
		FailureMessage: validation.ErrorMessage,
		Refs:           validation.Refs,
	}, diags
}

// evalCheckErrorMessage makes a best effort to evaluate the given expression,
// as an error message string.
//
// It will either return a non-empty message string or it'll return diagnostics
// with either errors or warnings that explain why the given expression isn't
// acceptable.
func evalCheckErrorMessage(expr hcl.Expression, hclCtx *hcl.EvalContext) (string, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	val, hclDiags := expr.Value(hclCtx)
	diags = diags.Append(hclDiags)
	if hclDiags.HasErrors() {
		return "", diags
	}

	val, err := convert.Convert(val, cty.String)
	if err != nil {
		diags = diags.Append(&hcl.Diagnostic{
			Severity:    hcl.DiagError,
			Summary:     "Invalid error message",
			Detail:      fmt.Sprintf("Unsuitable value for error message: %s.", tfdiags.FormatError(err)),
			Subject:     expr.Range().Ptr(),
			Expression:  expr,
			EvalContext: hclCtx,
		})
		return "", diags
	}
	if !val.IsKnown() {
		return "", diags
	}
	if val.IsNull() {
		diags = diags.Append(&hcl.Diagnostic{
			Severity:    hcl.DiagError,
			Summary:     "Invalid error message",
			Detail:      "Unsuitable value for error message: must not be null.",
			Subject:     expr.Range().Ptr(),
			Expression:  expr,
			EvalContext: hclCtx,
		})
		return "", diags
	}

	val, valMarks := val.Unmark()
	if _, sensitive := valMarks[marks.Sensitive]; sensitive {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagWarning,
			Summary:  "Error message refers to sensitive values",
			Detail: `The error expression used to explain this condition refers to sensitive values, so Terraform will not display the resulting message.

You can correct this by removing references to sensitive values, or by carefully using the nonsensitive() function if the expression will not reveal the sensitive data.`,
			Subject:     expr.Range().Ptr(),
			Expression:  expr,
			EvalContext: hclCtx,
		})
		return "", diags
	}

	// NOTE: We've discarded any other marks the string might have been carrying,
	// aside from the sensitive mark.

	return strings.TrimSpace(val.AsString()), diags
}
