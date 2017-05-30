package aws

import (
	"bytes"
	"fmt"
	"log"
	"regexp"

	"github.com/aws/aws-sdk-go/service/elasticbeanstalk"
	"github.com/hashicorp/terraform/helper/hashcode"
	"github.com/hashicorp/terraform/helper/schema"
)

func dataSourceAwsElasticBeanstalkSolutionStack() *schema.Resource {
	return &schema.Resource{
		Read: dataSourceAwsElasticBeanstalkSolutionStackRead,

		Schema: map[string]*schema.Schema{
			"name_regex": {
				Type:         schema.TypeString,
				Optional:     true,
				ForceNew:     true,
				ValidateFunc: validateNameRegex,
			},
			"most_recent": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
				ForceNew: true,
			},
		},
	}
}

// dataSourceAwsElasticBeanstalkSolutionStackRead performs the API lookup.
func dataSourceAwsElasticBeanstalkSolutionStackRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).elasticbeanstalkconn

	nameRegex, nameRegexOk := d.GetOk("name_regex")

	if !nameRegexOk {
		return fmt.Errorf("name_regex must be assigned")
	}

	resp, err := conn.ListAvailableSolutionStacksRequest()
	if err != nil {
		return err
	}

	var filteredSolutionStacks []*string

	r := regexp.MustCompile(nameRegex.(string))
	for _, solutionStack := range resp.SolutionStacks {
		if r.MatchString(*solutionStack) {
			filteredSolutionStacks = append(filteredSolutionStacks, solutionStack)
		}
	}

	var solutionStack *string
	if len(filteredSolutionStacks) < 1 {
		return fmt.Errorf("Your query returned no results. Please change your search criteria and try again.")
	}

	if len(filteredSolutionStacks) > 1 {
		recent := d.Get("most_recent").(bool)
		log.Printf("[DEBUG] aws_ami - multiple results found and `most_recent` is set to: %t", recent)
		if recent {
			solutionStack = mostRecentSolutionStack(filteredSolutionStacks)
		} else {
			return fmt.Errorf("Your query returned more than one result. Please try a more " +
				"specific search criteria, or set `most_recent` attribute to true.")
		}
	} else {
		// Query returned single result.
		solutionStack = filteredSolutionStacks[0]
	}

	log.Printf("[DEBUG] aws_elastic_beanstalk_solution_stack - Single solution stack found: %s", *solutionStack)
	return solutionStackDescriptionAttributes(d, solutionStack)
}

// Returns the most recent solution stack out of a slice of stacks.
func mostRecentSolutionStack(solutionStacks []*string) *string {
	return solutionStacks[0]
}

// populate the numerous fields that the image description returns.
func solutionStackDescriptionAttributes(d *schema.ResourceData, solutionStack *string) error {
	// Simple attributes first
	d.SetId(*solutionStack)
	d.Set("name", solutionStack)
	return nil
}

func validateNameRegex(v interface{}, k string) (ws []string, errors []error) {
	value := v.(string)

	if _, err := regexp.Compile(value); err != nil {
		errors = append(errors, fmt.Errorf(
			"%q contains an invalid regular expression: %s",
			k, err))
	}
	return
}
