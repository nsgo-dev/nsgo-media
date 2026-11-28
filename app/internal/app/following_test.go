package app

import (
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/nsgo-dev/nsgo-media/app/internal/store"
)

// pick chooses the item id, labelled label, of the menu opened by
// pressing trigger.
func pick(app *nsgotest.App, trigger, id, label string) {
	app.Press("id:" + trigger)
	if app.Exists("id:" + id) {
		app.Press("id:" + id)
	} else {
		// On macOS, the menu is the system's.
		app.ChoosePopup(label)
	}
}

func signOutOfDemo(app *nsgotest.App, pl *player) {
	p := pl.sh.plugins.GetUntracked()[0]
	app.Post(func() { pl.sh.signOut(p) })
	app.WaitUntil(wait, func() bool { return pl.sh.account(p).who.GetUntracked() == nil })
}

func TestFollowing(t *testing.T) {
	app, pl := startDemo(t, "")

	// Following lives on the account: signed out, it asks to sign in.
	app.Press("id:nav-following")
	app.WaitFor("text:Sign in to see the shows you follow", wait)
	app.Press("id:error-sign-in")
	app.WaitFor("text:You're not following anything yet", wait)
	app.Find("text:Follow a show from its page on Demo, and its new episodes show up here.")

	// A show's video follows it, and goes on after the episode watched.
	app.Press("id:nav-home")
	app.WaitFor("id:item-video-v4", wait)
	app.Press("id:item-video-v4")
	app.WaitFor("id:play-video >> text:Watch EP 3", wait)
	app.Find("text:Episodes")
	app.Find("id:part-1 >> text:EP 1")
	if app.Exists("id:following-badge") {
		t.Fatal("a badge before following anything")
	}
	app.Press("id:follow")
	app.WaitFor("id:follow-menu", wait)
	app.WaitFor("id:follow-menu >> text:Watching", wait)
	app.WaitFor("id:following-badge >> text:1", wait)

	pick(app, "follow-menu", "status-completed", "Completed")
	app.WaitFor("id:follow-menu >> text:Completed", wait)

	// The following page shows it with its new episodes, and by status.
	app.Press("id:nav-following")
	app.WaitFor("id:shows-new >> id:show-s1", wait)
	app.Find("id:show-s1 >> text:Watched EP 2")
	app.Find("id:show-s1 >> text:Updated to EP 4")
	app.Find("id:show-s1 >> id:new")
	app.Find("id:show-s1 >> id:source-demo")
	app.Press("id:status-planned")
	app.WaitFor("text:Nothing here", wait)
	app.Press("id:status-completed")
	app.WaitFor("id:shows-completed >> id:show-s1", wait)
	app.Press("id:status-all")
	app.WaitFor("id:shows-new >> id:show-s1", wait)

	// Its card opens the show's video, which plays the next episode.
	app.Press("id:show-s1")
	app.WaitFor("id:play-video >> text:Watch EP 3", wait)
	app.Press("id:play-video")
	app.WaitUntil(wait, func() bool { return title(app, pl) == "Night market stories – Last orders" })
	closePlayer(app, pl)

	// Unfollowing goes back to Follow, and the badge goes.
	pick(app, "follow-menu", "unfollow", "Unfollow")
	app.WaitFor("id:follow", wait)
	app.WaitForGone("id:following-badge", wait)

	// A follow that fails goes back and says why, offering to sign in.
	signOutOfDemo(app, pl)
	app.WaitFor("id:follow", wait)
	app.Press("id:follow")
	app.WaitFor("text:Could not follow", wait)
	app.Find("text:Sign in to change what you follow")
	app.WaitFor("id:follow", wait)
	if app.Exists("id:follow-menu") {
		t.Fatal("a follow that failed still shows the show followed")
	}
}

// TestFollowingLoadsAndFails shows the following page loading, then its
// error, and the shows once tried again.
func TestFollowingLoadsAndFails(t *testing.T) {
	data := t.TempDir()
	flaky := fstest.MapFS{
		"plugin.yaml": {Data: []byte("id: flaky\nname: Flaky\nversion: 1.0.0\nminAppVersion: 0.1.0\nmain: index.js\nmedia: video\n" +
			"statuses:\n  - { id: watching, label: Watching }\n")},
		// The toolbar's badge asks first, then the page; both fail.
		"index.js": {Data: []byte(`let calls = 0;
export async function following() {
  await new Promise((resolve) => setTimeout(resolve, 300));
  if (calls++ < 2) throw new Error("The site is down");
  return { shows: [{ id: "s", title: "A show", video: "v", status: "watching", watched: 1, episodes: 3 }] };
}`)},
	}
	if err := store.InstallPlugin(filepath.Join(data, "plugins"), "flaky", flaky); err != nil {
		t.Fatal(err)
	}
	app, _ := start(t, data)
	app.Press("id:nav-following")
	app.WaitFor("id:page-loading", wait)
	app.WaitFor("text:The site is down", wait)
	waitText(app, "state-title", "Flaky could not answer")
	app.Press("id:retry")
	app.WaitFor("id:show-s >> text:Watched EP 1", wait)
	app.Find("id:show-s >> text:3 episodes")
	if app.Exists("id:status") {
		t.Fatal("status tabs for a plugin of one status")
	}
}
