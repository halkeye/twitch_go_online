package main

import (
	"fmt"
	"os"

	"github.com/halkeye/twitch_go_online/internal/airtable"
)

func main() {
	at := airtable.New(os.Getenv("AIRTABLE_API_KEY"), "app9gXc0ovBSGKOSE", "halkeye")
	err := at.RegisterWebhook("https://dev.g4v.dev/webhook/airtable")
	if err != nil {
		panic(err)
	}
	usernames, err := at.Usernames()
	if err != nil {
		panic(err)
	}
	fmt.Println(usernames)

	/*
		client := airtable.NewClient(os.Getenv("AIRTABLE_API_KEY"))
		records, err := client.GetTable("app9gXc0ovBSGKOSE", "halkeye").GetRecordsWithParams(url.Values{})
		if err != nil {
			panic(err)
		}
		for _, record := range records.Records {
			fmt.Println(record.Fields["Twitch Account"])
		}
	*/
	/*
		// client.SetCustomClient(http.DefaultClient)
		bases, err := client.GetBases().WithOffset("").Do()
		if err != nil {
			panic(err)
		}
		for _, base := range bases.Bases {
			fmt.Println(base)

			schema, err := client.GetBaseSchema(base.ID).Do()
			if err != nil {
				panic(err)
			}

			for _, table := range schema.Tables {
				fmt.Println(table)

				records, err := client.GetTable(base.ID, table.Name).GetRecordsWithParams(url.Values{})
				if err != nil {
					panic(err)
				}
				for _, record := range records.Records {
					fmt.Println(record.Fields["Twitch Account"])
				}
			}
		}
	*/

}
