Output docs/assets/demo.gif
Set Theme "Catppuccin Mocha"
Set FontSize 15
Set Width 1400
Set Height 700
Set Padding 20
Set TypingSpeed 40ms
Set Shell "bash"

# --- ACT 1: Show the intent (5-6s) ---

Type "# The intent we declared for demo-app:"
Enter
Sleep 400ms
Type "kubectl get appintent demo-app -o yaml | sed -n '/^spec:/,/^status:/p' | head -15"
Enter
Sleep 4s

# --- ACT 2: Show the baseline (4-5s) ---

Type "# Current state — all green:"
Enter
Sleep 400ms
Type "kubectl get appintent demo-app"
Enter
Sleep 3s

# --- ACT 3: Break it and watch it react (the heart of the demo) ---

Type "# Pushing traffic — watch the operator react:"
Enter
Sleep 400ms
Type "kubectl run load-test --image=williamyeh/hey --restart=Never -- -z 60s -c 30 http://demo-app.default.svc.cluster.local &"
Enter
Sleep 2s
Type "kubectl get appintent demo-app -w"
Enter
Sleep 60s
Ctrl+C
Sleep 400ms

# --- ACT 4: Show the decision (5-6s) ---

Type "clear"
Enter
Sleep 300ms
Type "# Every decision the operator made, with its reasoning:"
Enter
Sleep 400ms
Type "kubectl get appintent demo-app -o jsonpath='{.status.decisions[-1:]}' | python3 -m json.tool"
Enter
Sleep 6s

# --- Cleanup (not visible in gif) ---

Type "kubectl delete pod load-test --ignore-not-found > /dev/null 2>&1"
Enter
Sleep 500ms
