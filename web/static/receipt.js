// Quittung scannen: Foto im Browser verkleinern, an /api/receipt schicken
// und das Buchungsformular mit Betrag, Datum, Händler und Kategorie füllen.
(function () {
    var input = document.getElementById('receipt-photo');
    var form = document.getElementById('tx-new-form');
    if (!input || !form) return;
    var status = document.getElementById('receipt-status');
    var textBox = document.getElementById('receipt-text-box');
    var textPre = document.getElementById('receipt-text');
    var btn = input.closest('.receipt-btn');

    // Lange Seite auf 2000 px, Graustufen: reicht Tesseract für Bon-Schrift,
    // lädt über VPN schnell hoch und rechnet auf dem Pi viel kürzer.
    function shrink(file) {
        return new Promise(function (resolve) {
            var url = URL.createObjectURL(file);
            var img = new Image();
            img.onload = function () {
                var max = 2000, w = img.naturalWidth, h = img.naturalHeight;
                var s = Math.min(1, max / Math.max(w, h));
                var c = document.createElement('canvas');
                c.width = Math.round(w * s);
                c.height = Math.round(h * s);
                var ctx = c.getContext('2d');
                ctx.filter = 'grayscale(1) contrast(1.3)';
                ctx.drawImage(img, 0, 0, c.width, c.height);
                URL.revokeObjectURL(url);
                c.toBlob(function (b) { resolve(b || file); }, 'image/jpeg', 0.85);
            };
            img.onerror = function () { URL.revokeObjectURL(url); resolve(file); };
            img.src = url;
        });
    }

    function set(name, value) {
        var el = form.elements[name];
        if (!el || !value) return false;
        if (el.tagName === 'SELECT' && !Array.prototype.some.call(el.options, function (o) { return o.value === value; })) {
            el.add(new Option(value, value));
        }
        el.value = value;
        el.classList.add('receipt-filled');
        el.dispatchEvent(new Event('change', { bubbles: true }));
        return true;
    }

    function fill(res) {
        set('kind', 'expense');
        var found = [];
        if (set('amount', res.amount ? res.amount.toFixed(2) : '')) found.push('Betrag');
        if (set('booked_at', res.date)) found.push('Datum');
        if (set('description', res.description)) found.push('Händler');
        if (set('category', res.category)) found.push('Kategorie');
        var missing = ['Betrag', 'Datum', 'Händler'].filter(function (f) { return found.indexOf(f) < 0; });
        status.textContent = found.length
            ? '✓ Erkannt: ' + found.join(', ') + '. Bitte kurz prüfen' + (missing.length ? ' und ' + missing.join(', ') + ' ergänzen.' : '.')
            : (res.error || 'Nichts erkannt.');
        if (!res.amount) {
            form.elements.amount.focus();
        }
    }

    input.addEventListener('change', function () {
        var file = input.files && input.files[0];
        if (!file) return;
        btn.classList.add('busy');
        status.textContent = '⏳ Lese Quittung … (dauert ein paar Sekunden)';
        textBox.hidden = true;
        shrink(file).then(function (blob) {
            var fd = new FormData();
            fd.append('photo', blob, 'quittung.jpg');
            return fetch('/api/receipt', { method: 'POST', body: fd, credentials: 'same-origin' });
        }).then(function (r) {
            if (r.redirected || (r.headers.get('Content-Type') || '').indexOf('json') < 0) {
                throw new Error('Bitte neu anmelden.');
            }
            return r.json();
        }).then(function (res) {
            if (res.text) {
                textPre.textContent = res.text;
                textBox.hidden = false;
            }
            if (res.error && !res.amount && !res.date && !res.description) {
                status.textContent = '⚠️ ' + res.error;
                return;
            }
            fill(res);
        }).catch(function (e) {
            status.textContent = '⚠️ ' + (e.message || 'Hochladen fehlgeschlagen.');
        }).finally(function () {
            btn.classList.remove('busy');
            input.value = '';
        });
    });
})();
