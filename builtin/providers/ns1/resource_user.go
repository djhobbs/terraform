package ns1

import (
	"log"

	"github.com/hashicorp/terraform/helper/schema"
	ns1 "gopkg.in/ns1/ns1-go.v2/rest"
	"gopkg.in/ns1/ns1-go.v2/rest/model/account"
)

func userResource() *schema.Resource {
	return &schema.Resource{
		Create: resourceNS1UserCreate,
		Read:   resourceNS1UserRead,
		Update: resourceNS1UserUpdate,
		Delete: resourceNS1UserDelete,
		Schema: map[string]*schema.Schema{
			"id": &schema.Schema{
				Type:     schema.TypeString,
				Computed: true,
			},
			"name": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
			},
			"username": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
			},
			"email": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
			},
			"notify": &schema.Schema{
				Type:     schema.TypeMap,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"billing": &schema.Schema{
							Type:     schema.TypeBool,
							Required: true,
						},
					},
				},
			},
			"teams": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"permissions": permissionsSchema(),
		},
	}
}

func resourceNS1UserCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ns1.Client)

	u := buildNS1User(d)

	log.Printf("[INFO] Creating NS1 user: %s \n", u.Name)

	if _, err := client.Users.Create(u); err != nil {
		return err
	}

	d.SetId(u.Username)

	return resourceNS1UserRead(d, meta)
}

func resourceNS1UserRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ns1.Client)

	log.Printf("[INFO] Reading NS1 user: %s \n", d.Id())

	u, _, err := client.Users.Get(d.Id())
	if err != nil {
		return err
	}

	d.Set("name", u.Name)
	d.Set("email", u.Email)
	d.Set("teams", u.TeamIDs)

	notify := make(map[string]bool)
	notify["billing"] = u.Notify.Billing
	d.Set("notify", notify)

	return nil
}

func resourceNS1UserUpdate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ns1.Client)

	u := buildNS1User(d)

	log.Printf("[INFO] Updating NS1 user: %s \n", u.Name)

	if _, err := client.Users.Update(u); err != nil {
		return err
	}

	return nil
}

func resourceNS1UserDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*ns1.Client)

	log.Printf("[INFO] Deleting NS1 user: %s \n", d.Id())

	if _, err := client.Users.Delete(d.Id()); err != nil {
		return err
	}

	d.SetId("")

	return nil
}

func buildNS1User(d *schema.ResourceData) *account.User {
	u := &account.User{
		Name:     d.Get("name").(string),
		Username: d.Get("username").(string),
		Email:    d.Get("email").(string),
	}

	if v, ok := d.GetOk("teams"); ok {
		teamsRaw := v.([]interface{})
		u.TeamIDs = make([]string, len(teamsRaw))
		for i, team := range teamsRaw {
			u.TeamIDs[i] = team.(string)
		}
	} else {
		u.TeamIDs = make([]string, 0)
	}

	if v, ok := d.GetOk("notify"); ok {
		notifyRaw := v.(map[string]interface{})
		u.Notify.Billing = notifyRaw["billing"].(bool)
	}

	return u
}
