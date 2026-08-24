(() => {
    "use strict";

    function applyZoraxyTheme() {
        let dark = false;
        try {
            dark = localStorage.getItem("theme") === "dark";
        } catch {
            // Keep the standalone light theme when storage is unavailable.
        }
        document.body.classList.toggle("darkTheme", dark);
    }

    applyZoraxyTheme();

    window.addEventListener("storage", event => {
        if (event.key === "theme") applyZoraxyTheme();
    });
    window.addEventListener("pageshow", applyZoraxyTheme);
    document.addEventListener("visibilitychange", () => {
        if (!document.hidden) applyZoraxyTheme();
    });
})();
