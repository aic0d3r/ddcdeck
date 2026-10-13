// Report the active window's class to ddcdeckd. Loaded by ddcdeck via the KWin
// scripting D-Bus API; reloaded automatically when KWin restarts.
function report(w) {
    callDBus("io.github.aic0d3r.ddcdeckd",
             "/io/github/aic0d3r/ddcdeckd/app",
             "io.github.aic0d3r.ddcdeckd",
             "SetClass",
             w ? String(w.resourceClass) : "");
}
workspace.windowActivated.connect(report);
report(workspace.activeWindow);
