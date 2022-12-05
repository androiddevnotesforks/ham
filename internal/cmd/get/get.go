package get

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/mkideal/cli"

	"github.com/charmbracelet/lipgloss"

	"github.com/antony-jr/ham/internal/banner"
	"github.com/antony-jr/ham/internal/core"
	"github.com/antony-jr/ham/internal/helpers"
	"github.com/hetznercloud/hcloud-go/hcloud"
)

// TODO: Update this once made a public build available. We need
// to point this url to the latest linux build for the server to
// use.
const (
	HAM_LINUX_BINARY_URL string = "https://github.com/antony-jr/ham/releases/latest"
)

type getT struct {
	cli.Helper

	NoConfirm               bool   `cli:"n,no-confirm" usage:"Auto Confirm 'Yes' to all questions for the user. (Use with Caution)"`
	KeepServer              bool   `cli:"k,keep-server" usage:"Don't Destroy the Remote Server on any error."`
	KeepServerOnConnectFail bool   `cli:"s,keep-server-conn-fail" usage:"Don't Destroy the Remote Server even if we can't SSH into it."`
	KeepServerOnTrackFail   bool   `cli:"t,keep-server-track-fail" usage:"Don't Destroy the Remote Server even if Tracking Fails."`
	KeepServerOnBuildFail   bool   `cli:"b,keep-server-build-fail" usage:"Don't Destroy the Remote Server even if Build Fails. (Use with Caution)"`
	TestingSSHIP            string `cli:"i,testing-ssh-ip" usage:"Run a Test Run without Creating Servers and Use the given IP as Build Server. (Developer)"`
	Force                   bool   `cli:"f,force" usage:"Force start a build even if the recipe was built Already."`
}

func ParseGitRemoteString(remote string) (string, string) {
	urlSlice := strings.Split(remote, "://")

	if len(urlSlice) == 2 {
		remote = urlSlice[1]
	}

	slice := strings.Split(remote, "/")

	branchSlice := strings.Split(remote, ":")
	branch := ""
	url := ""

	if len(branchSlice) == 2 {
		url = branchSlice[0]
		branch = branchSlice[1]
	} else {
		url = remote
	}

	if len(urlSlice) == 2 {
		url = fmt.Sprintf("%s://%s", urlSlice[0], url)
	}

	if len(slice) != 2 {
		return url, branch
	}

	user := slice[0]
	userSlice := strings.Split(user, "@")

	if len(userSlice) != 2 {
		return url, branch
	}

	uname := userSlice[0]
	host := userSlice[1]

	if strings.ToLower(host) != "gh" {
		return url, branch
	}

	repo := slice[1]
	repoSlice := strings.Split(repo, ":")

	if len(repoSlice) == 2 {
		repo = repoSlice[0]
	}

	// Official HAM Recipes.
	if uname == "~" {
		return fmt.Sprintf("https://github.com/ham-community/%s", repo), branch
	}

	return fmt.Sprintf("https://github.com/%s/%s", uname, repo), branch
}

func NewCommand() *cli.Command {
	return &cli.Command{
		Name: "get",
		Desc: "Get a build of AOSP from community recipe or locally using your Hetzner Cloud",
		Text: `
Syntax: ham get [RECIPE LOCATION]

Recipe from Ham Community:
   ham get ~@gh/enchilada_los18.1
   ham get ~@gh/enchilada_los18.1:bleeding

Recipe from Github:
   ham get user@gh/repo:branch
   ham get antony-jr@gh/enchilada_los18.1
   ham get antony-jr@gh/ecnhilada_los18.1:dev

Recipe from Git:
   ham get https://antonyjr.in/enchilada_los181.git

Local Recipe:
   ham get ./examples/enchilada_los18.1`,
		Argv: func() interface{} { return new(getT) },
		NumArg: func(n int) bool {
			if n != 1 {
				return false
			}
			return true
		},
		Fn: func(ctx *cli.Context) error {
			argv := ctx.Argv().(*getT)
			args := ctx.Args()
			if len(args) != 1 {
				return nil
			}
			recipe_src := args[0]
			dir := recipe_src
			testingRun := len(argv.TestingSSHIP) != 0

			if testingRun {
				if runtime.GOOS != "linux" {
					return errors.New("OS Not Supported for Testing. Get a Linux Machine to Develop HAM.")
				}
			}

			checkMark := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).SetString("✓")
			optionalSuffix := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).SetString(" (OPTIONAL, Press ENTER to Skip)")

			banner.GetStartBanner()

			fmt.Printf(" %s Parsing %s...\n", checkMark, recipe_src)
			remove := false
			usedGit := false
			gitUrl := ""
			gitBranch := ""
			if _, err := os.Stat(recipe_src); os.IsNotExist(err) {
				// Recipe is not local, so use git to clone the
				// the recipe requested by the user.
				banner.GetRecipeNotExistsBanner()

				// Parse the string
				git_url, git_branch := ParseGitRemoteString(recipe_src)
				orig_branch := git_branch

				if git_branch == "" {
					git_branch = "default"
				}

				fmt.Printf(" %s Git URL: %s\n", checkMark, git_url)
				fmt.Printf(" %s Git Branch: %s\n", checkMark, git_branch)

				uniqueTempDir, err := os.MkdirTemp(os.TempDir(), "*-ham-recipe")
				if err != nil {
					return err
				}
				dir = uniqueTempDir
				remove = true
				usedGit = true
				gitUrl = git_url
				gitBranch = orig_branch

				fmt.Printf(" %s Cloning Into: %s\n", checkMark, dir)

				r, err := git.PlainClone(dir, false, &git.CloneOptions{
					URL: git_url,
				})

				if err != nil {
					_ = os.RemoveAll(dir)
					return err
				}

				if orig_branch != "" {
					w, err := r.Worktree()
					if err != nil {
						_ = os.RemoveAll(dir)
						return err
					}

					err = w.Checkout(&git.CheckoutOptions{Branch: plumbing.ReferenceName(orig_branch)})
					if err != nil {
						_ = os.RemoveAll(dir)
						return err
					}
				}
			}

			if remove {
				defer os.RemoveAll(dir)
			}

			if testingRun {
				fmt.Printf(" ! RUNNING IN TESTING MODE ! \n")
			}

			// Parse recipe file for meta information
			// and args information.
			hf, err := core.NewHAMFile(dir)
			if err != nil {
				return err
			}
			serverName := helpers.ServerNameFromSHA256(hf.SHA256Sum)

			banner.GetRecipeBanner(hf.Title, hf.Version, hf.SHA256Sum)

			config, err := core.GetConfiguration()
			if err != nil {
				return err
			}
			fmt.Printf(" %s Reading Configuration\n", checkMark)

			client := hcloud.NewClient(hcloud.WithToken(config.APIKey))
			fmt.Printf(" %s Connected with Hetzner Cloud API\n", checkMark)

			sshkeys, err := client.SSHKey.All(
				context.Background(),
			)
			if err != nil {
				return err
			}

			// Search for ham-ssh-key SSH Key,
			// if it does not exists then error out
			// asking the user to properly init.
			keyOk := false
			keyFingerprint, err := helpers.GetSSHFingerprint(config.SSHPublicKey)

			if err != nil {
				return err
			}

			var ham_labels map[string]string

			var hamSSHKey *hcloud.SSHKey

			for _, el := range sshkeys {
				if el.Name == "ham-ssh-key" {
					if keyFingerprint == el.Fingerprint {
						keyOk = true
						ham_labels = el.Labels
						hamSSHKey = el
					}
					break
				}
			}

			if !keyOk {
				return errors.New("HAM SSH Key not found, Please Re-Initialize.")
			}

			fmt.Printf(" %s Verified SSH Keys\n", checkMark)

			// Destroy all dead servers
			// whenver we see them.
			// This is highly unlikely that our ham leaves dead servers
			// but this is just a precaution.
			err = helpers.DestroyAllDeadServers(&client.Server)
			if err != nil {
				return err
			}

			// Search for build servers that were already started
			// if found, track that.
			servers, err := client.Server.All(
				context.Background(),
			)
			if err != nil {
				return err
			}

			var currentBuildServer *hcloud.Server
			serverRunning := false
			for _, server := range servers {
				if server.Name == serverName {
					// Track status instead of creating a new one.
					currentBuildServer = server
					fmt.Printf(" %s Active Build Found\n", checkMark)
					serverRunning = true
					break
				}
			}

			if testingRun {
				serverRunning = false
			}

			// This is a safety net.
			destroyServer := !argv.KeepServer
			defer deferDeleteServer(&client.Server, &destroyServer, serverName)

			var ip6Addr string
			if testingRun {
				ip6Addr = argv.TestingSSHIP
			} else {
				if currentBuildServer != nil {
					ip6Addr = fmt.Sprintf("[%s]:22", string(currentBuildServer.PublicNet.IPv6.IP))
				} else {
					ip6Addr = "[::1]:22"
				}
			}

			if !serverRunning {
				// Check the ham-ssh-key labels, label with the server
				// name will have the last build status like success
				// or failed.
				for key, status := range ham_labels {
					if key == serverName && !argv.Force {
						// Confirm with user before starting the
						// build again.
						if testingRun {
							break
						}
						estr := fmt.Sprintf("A %s build had run before with this recipe, Run with -f flag to force build.",
							status)
						return errors.New(estr)
					}
					break
				}
				fmt.Printf(" %s Checked Previous Builds\n", checkMark)

				// Create a new build server.

				// Before that we need to get variables from the user
				// such as special files, env vars required for the
				// build from the user. This might be crucial secrets
				// so transport it with SSH to stay secure.
				buildVars := core.NewVariables()

				if len(hf.Args) != 0 {
					banner.GetQuestionBanner()

					for _, arg := range hf.Args {
						placeholder := "Value"
						required := false
						valueType := core.VARIABLE_TYPE_VALUE

						argType := strings.ToLower(arg.Type)
						if argType == "file" {
							placeholder = "File Path"
							valueType = core.VARIABLE_TYPE_FILE_PATH
						} else if argType == "secret" {
							placeholder = "Secret"
							valueType = core.VARIABLE_TYPE_SECRET
						}

						if arg.Required != nil {
							required = *arg.Required
						}

						questionResponse := NewQuestionResponse(required, valueType == core.VARIABLE_TYPE_SECRET)

						suffix := ""
						if !required {
							suffix = fmt.Sprintf("%s", optionalSuffix)
						}

						runQuestionTeaProgram(questionResponse, arg.Prompt+suffix, placeholder)

						if questionResponse.err != nil {
							return questionResponse.err
						}

						buildVars.PutVar(arg.ID, questionResponse.answer, valueType)
						fmt.Println()
					}
				}

				// Build the vars.json file and get ready to upload
				// to the server once created
				fileUploads := make(map[string]string)
				varsJson := make(map[string]string)
				fileIndex := 0
				for key, val := range buildVars.Vars {
					if val.Type == core.VARIABLE_TYPE_VALUE ||
						val.Type == core.VARIABLE_TYPE_SECRET {
						if len(val.Value) != 0 {
							varsJson[key] = val.Value
						}
					} else if val.Type == core.VARIABLE_TYPE_FILE_PATH {
						exists, err := helpers.FileExists(val.Value)
						if !exists {
							return errors.New("File given in Variables does not Exists (" + err.Error() + ")")
						}

						fileIndex++
						varsJson[key] = fmt.Sprintf("/ham-files/%d", fileIndex)
						fileUploads[val.Value] = varsJson[key]
					}
				}
				varsFilePath := fmt.Sprintf("%s%c%s-vars.json", os.TempDir(), os.PathSeparator, serverName)
				err = helpers.DumpJsonFile(varsJson, varsFilePath)
				if err != nil {
					return err
				}
				defer os.Remove(varsFilePath)

				// In testing we don't need to get price or confirmation
				// or creation of server.
				if !testingRun {

					// Get Suitable Server and Price
					price, serverType, err := GrossServerPriceForServerWithHighestPerformance(client)
					if err != nil {
						return err
					}
					banner.GetServerPriceInformationBanner(strings.ToUpper(serverType.Name), price)

					confirmCreate := argv.NoConfirm

					if !argv.NoConfirm {
						err = runConfirmCreateTeaProgram(&confirmCreate)
						if err != nil {
							return err
						}
					}

					// Keep this condition simple since this is an important
					// decision by the user.
					if confirmCreate == false {
						return errors.New("User Declined to Create a New Server.")
					} else {
						// ! IMPORTANT SECTION !
						// TODO: Create a server here
						// TODO: Set currentBuildServer here
						// TODO: ip6Addr = fmt.Sprintf("[%s]:22", string(currentBuildServer.PublicNet.IPv6.IP))

					}
				}

				// Install HAM Linux Binary to the Server
				sshShellClient, err := GetSSHClient(ip6Addr, config.SSHPrivateKey)
				if err != nil {
					return err
				}
				defer sshShellClient.Close()
				sshSftpClient, err := GetSSHClient(ip6Addr, config.SSHPrivateKey)
				if err != nil {
					return err
				}
				defer sshSftpClient.Close()

				sftpClient, err := helpers.GetSFTPClient(sshSftpClient)
				if err != nil {
					return err
				}
				defer sftpClient.Close()

				shell, err := GetSSHShell(sshShellClient)
				if err != nil {
					return err
				}

				_, err = shell.Exec("DEBIAN_FRONTEND=noninteractive apt-get update -y -qq")
				_, err = shell.Exec("DEBIAN FRONTEND=noninteractive apt-get upgrade -y -qq")
				_, err = shell.Exec("DEBIAN FRONTEND=noninteractive apt-get install -y -qq git wget curl")

				if runtime.GOOS != "linux" && !testingRun {
					_, err = shell.Exec(fmt.Sprintf("wget -O /usr/bin/ham \"%s\"", HAM_LINUX_BINARY_URL))
				} else {
					err = helpers.SFTPCopyFileToRemote(sftpClient, "/usr/bin/ham", os.Args[0])
				}
				_, err = shell.Exec("chmod a+x /usr/bin/ham")

				if err != nil {
					return err
				}

				// Copy HAM Configuration File
				configFilePath, err := helpers.ConfigFilePath()
				if err != nil {
				   return err
				}
				err = helpers.SFTPCopyFileToRemote(sftpClient, "/root/.ham.json", configFilePath)
				if err != nil {
				   return err
				}

				// Make required directories
				_, err = shell.Exec("mkdir -p /ham-build")
				_, err = shell.Exec("mkdir -p /ham-recipe")
				_, err = shell.Exec("mkdir -p /ham-files")

				if err != nil {
					return err
				}

				// Upload recipe repo (with SCP) or make the server download it.
				if usedGit {
					_, err = shell.Exec("rm -rf /ham-recipe")
					_, err = shell.Exec("cd /")
					_, err = shell.Exec(fmt.Sprintf("git clone %s ham-recipe", gitUrl))
					_, err = shell.Exec("cd ham-recipe")
					if gitBranch != "" {
						_, err = shell.Exec(fmt.Sprintf("git checkout -b %s", gitBranch))
					}
				} else {

					// TODO: Make sure that it does not depend on trailing / for
					// local recipes
					rootDir := dir

					walker := func(path string, info os.FileInfo, err error) error {
						if err != nil {
							return err
						}

						destFile := strings.ReplaceAll(path, rootDir, "")

						if info.IsDir() {
							return sftpClient.MkdirAll(fmt.Sprintf("/ham-recipe/%s", destFile))
						}

						return helpers.SFTPCopyFileToRemote(sftpClient, fmt.Sprintf("/ham-recipe/%s", destFile), path)
					}

					err = filepath.Walk(rootDir, walker)
					if err != nil {
						return err
					}
				}

				if err != nil {
					return err
				}

				// Upload files from vars.json to server using SFTP securely.
				for srcFilePath, destFilePath := range fileUploads {
					err = helpers.SFTPCopyFileToRemote(sftpClient, destFilePath, srcFilePath)
					if err != nil {
						return err
					}
				}

				// Upload the vars.json file
				err = helpers.SFTPCopyFileToRemote(sftpClient, fmt.Sprintf("/ham-files/vars.json"), varsFilePath)
				if err != nil {
					return err
				}

				// Start HAM build since we newly created this server
				keep := ""
				if argv.KeepServer || argv.KeepServerOnBuildFail {
					keep = "--keep-server"
				}
				buildCommand := fmt.Sprintf("ham build %s --sum %s --recipe /ham-recipe --vars /ham-files/vars.json",
					keep,
					hf.SHA256Sum)

				_, err = shell.Exec(buildCommand)
				if err != nil {
					return err
				}

				time.Sleep(time.Second * time.Duration(10))

				sftpClient.Close()
				sshSftpClient.Close()
				sshShellClient.Close()
			}

			tries := 0
			for {
				sshCode, err := trackRemoteServerProgress(ip6Addr, config.SSHPrivateKey)

				// Check for SSH Shell Code for More
				// accurate errors.
				if sshCode != SSH_SHELL_NO_ERROR {
					if sshCode == SSH_SHELL_CANNOT_GET_CLIENT ||
						sshCode == SSH_SHELL_CANNOT_GET_SESSION ||
						sshCode == SSH_SHELL_CANNOT_CONNECT {
						tries++
						if tries >= 3 {
							if argv.KeepServer || argv.KeepServerOnConnectFail {
								destroyServer = false
								banner.GetConnectFailBanner(serverName)
								return errors.New(
									"Cannot Get SSH Client (" + err.Error() + "), But Server is Kept and Still Running.")
							}

							delErr := helpers.TryDeleteServer(&client.Server, serverName, 20, 5)
							if delErr != nil {
								banner.GetConnectFailBanner(serverName)
								return delErr
							}

							destroyServer = false
							return errors.New("Cannnot Get SSH Client (" + err.Error() + "). Destroyed Server.")
						}

						time.Sleep(time.Second * time.Duration(5))
						continue
					} else if sshCode == SSH_SHELL_MALFORMED_JSON {
						if argv.KeepServer || argv.KeepServerOnTrackFail {
							destroyServer = false
							banner.GetMalformedJSONBanner(serverName)
							return errors.New("Malformed JSON from Build Server, But Server is Kept and Still Running.")
						}

						tries++
						if tries < 10 {
							fmt.Println(" Retrying in 10 mins... ")
							time.Sleep(time.Minute * time.Duration(10))
							continue
						}

						delErr := helpers.TryDeleteServer(&client.Server, serverName, 20, 5)
						if delErr != nil {
							banner.GetMalformedJSONBanner(serverName)
							return delErr
						}

						destroyServer = false
						return errors.New("Malformed JSON from Build Server. Destroyed Server")
					} else if sshCode == SSH_SHELL_HAM_STATUS_ERRORED {
						if argv.KeepServer || argv.KeepServerOnBuildFail {
							destroyServer = false
							banner.GetBuildFailedBanner(serverName)
							return errors.New("Remote Build Failed, But Server is Kept and Still Running.")
						}

						delErr := helpers.TryDeleteServer(&client.Server, serverName, 20, 5)
						if delErr != nil {
							banner.GetBuildFailedBanner(serverName)
							return delErr
						}

						destroyServer = false
						return errors.New("Remote Build Failed. Destroyed Server.")
					} else {
						tries++
						if tries >= 3 {
							delErr := helpers.TryDeleteServer(&client.Server, serverName, 20, 5)
							if delErr != nil {
								return delErr
							}

							destroyServer = false
							return errors.New("Unknown Build Error. Destroyed Server.")
						}
					}
				} else {
					if err != nil {
						return err
					}

					destroyServer = false
					break
				}

			}

			statusTries := 0
			for statusTries < 20 {
				statusTries++

				allSSHKeys, err := client.SSHKey.All(
					context.Background(),
				)
				if err != nil {
					time.Sleep(time.Second * time.Duration(10))
					continue
				}

				for _, key := range allSSHKeys {
					if key.Name == hamSSHKey.Name {
						labels := key.Labels
						for serv, buildStatus := range labels {
							if serv == serverName {
								if buildStatus == "successful" {
									fmt.Println("Build Successful")
								} else if buildStatus == "inprogress" {
									fmt.Println("Build in Progress")
								} else {
									destroyServer = !argv.KeepServer || !argv.KeepServerOnBuildFail
								}
								return nil
							}
						}
					}
				}

			}

			banner.GetMalformedJSONBanner(serverName)
			return errors.New("Cannot Get Status of Build.")
		},
	}
}

// This is defer delete, might come in handy when user exits the
// program with Ctrl+Z or some other means, as long it's not killed
// it can try to delete any created server. The state is checked if
// we have to delete the server since it may not be desired by the
// user.
func deferDeleteServer(sclient *hcloud.ServerClient, destroy *bool, serverName string) {
	if destroy != nil && *destroy {
		helpers.TryDeleteServer(sclient, serverName, 5, 5)
	}
}

func trackRemoteServerProgress(host string, sshPrivateKey string) (SSHShellCode, error) {
	sshClient, err := GetSSHClient(host, sshPrivateKey)
	if err != nil {
		return SSH_SHELL_CANNOT_GET_CLIENT, err
	}
	defer sshClient.Close()

	shell, err := GetSSHShell(sshClient)
	if err != nil {
		return SSH_SHELL_CANNOT_GET_SESSION, err
	}

	err = runProgressTeaProgram(shell)
	if err != nil {
		return shell.code, err
	}

	return shell.code, nil

}
