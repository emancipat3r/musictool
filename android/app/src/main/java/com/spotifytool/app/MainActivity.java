package com.spotifytool.app;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.http.SslError;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.webkit.SslErrorHandler;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;

import java.net.HttpURLConnection;
import java.net.InetAddress;
import java.net.NetworkInterface;
import java.net.URL;
import java.security.cert.X509Certificate;
import java.util.Collections;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

import javax.net.ssl.HttpsURLConnection;
import javax.net.ssl.SSLContext;
import javax.net.ssl.TrustManager;
import javax.net.ssl.X509TrustManager;

/**
 * Gated WebView over the spotifytool dashboard.
 *
 * Flow: probe the dashboard directly first (it is reachable from home wifi
 * without any VPN, and from anywhere when Tailscale is up via the subnet
 * router). Only when the probe fails do we look at Tailscale state and offer
 * to launch the Tailscale app. A network callback retries automatically the
 * moment the VPN comes up.
 *
 * The dashboard serves a self-signed certificate; TLS trust is relaxed ONLY
 * for the configured dashboard host, nothing else.
 */
public class MainActivity extends Activity {

    private static final String DEFAULT_URL = "https://192.168.12.56:8081";
    private static final String TAILSCALE_PKG = "com.tailscale.ipn";

    private final ExecutorService exec = Executors.newSingleThreadExecutor();
    private final Handler main = new Handler(Looper.getMainLooper());

    private WebView web;
    private LinearLayout gate;
    private TextView gateStatus;
    private ConnectivityManager.NetworkCallback netCallback;
    private boolean webLoaded = false;

    private SharedPreferences prefs;

    private String dashUrl() {
        return prefs.getString("url", DEFAULT_URL);
    }

    private String dashHost() {
        try {
            return new URL(dashUrl()).getHost();
        } catch (Exception e) {
            return "192.168.12.56";
        }
    }

    @Override
    protected void onCreate(Bundle b) {
        super.onCreate(b);
        prefs = getSharedPreferences("spotifytool", MODE_PRIVATE);
        buildUi();
        watchNetwork();
        probeAndRoute();
    }

    @SuppressLint("SetJavaScriptEnabled")
    private void buildUi() {
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(0xFF282828);

        web = new WebView(this);
        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true); // theme choice lives in localStorage
        web.setBackgroundColor(0xFF282828);
        web.setWebViewClient(new WebViewClient() {
            @Override
            public void onReceivedSslError(WebView v, SslErrorHandler handler, SslError error) {
                // Self-signed cert: accept for our pinned host only.
                if (error.getUrl() != null && error.getUrl().contains(dashHost())) {
                    handler.proceed();
                } else {
                    handler.cancel();
                }
            }

            @Override
            public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest req) {
                // Keep dashboard + terminal in-app; hand anything else
                // (open.spotify.com links etc.) to the system.
                String host = req.getUrl().getHost();
                if (host != null && host.equals(dashHost())) return false;
                try {
                    startActivity(new Intent(Intent.ACTION_VIEW, req.getUrl()));
                } catch (Exception ignored) {}
                return true;
            }
        });

        gate = new LinearLayout(this);
        gate.setOrientation(LinearLayout.VERTICAL);
        gate.setGravity(android.view.Gravity.CENTER);
        int pad = (int) (24 * getResources().getDisplayMetrics().density);
        gate.setPadding(pad, pad, pad, pad);

        TextView title = new TextView(this);
        title.setText("spotifytool");
        title.setTextColor(0xFFB49B72);
        title.setTextSize(28);
        title.setTypeface(null, android.graphics.Typeface.BOLD);
        gate.addView(title);

        gateStatus = new TextView(this);
        gateStatus.setTextColor(0xFFEBDBB2);
        gateStatus.setTextSize(15);
        gateStatus.setPadding(0, pad / 2, 0, pad);
        gate.addView(gateStatus);

        Button openTs = new Button(this);
        openTs.setText("Open Tailscale");
        openTs.setOnClickListener(v -> {
            Intent i = getPackageManager().getLaunchIntentForPackage(TAILSCALE_PKG);
            if (i != null) startActivity(i);
            else gateStatus.setText("Tailscale app not installed.");
        });
        gate.addView(openTs);

        Button retry = new Button(this);
        retry.setText("Retry");
        retry.setOnClickListener(v -> probeAndRoute());
        gate.addView(retry);

        Button setUrl = new Button(this);
        setUrl.setText("Dashboard URL…");
        setUrl.setOnClickListener(v -> promptUrl());
        gate.addView(setUrl);

        root.addView(gate, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.MATCH_PARENT));
        root.addView(web, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.MATCH_PARENT));
        web.setVisibility(View.GONE);
        setContentView(root);
    }

    private void promptUrl() {
        EditText input = new EditText(this);
        input.setText(dashUrl());
        new AlertDialog.Builder(this)
                .setTitle("Dashboard URL")
                .setView(input)
                .setPositiveButton("Save", (d, w) -> {
                    prefs.edit().putString("url", input.getText().toString().trim()).apply();
                    probeAndRoute();
                })
                .setNegativeButton("Cancel", null)
                .show();
    }

    /** Probe /healthz; show the WebView on success, the gate otherwise. */
    private void probeAndRoute() {
        gateStatus.setText("Checking dashboard…");
        exec.execute(() -> {
            boolean reachable = probeDashboard();
            boolean vpn = tailscaleActive();
            main.post(() -> {
                if (reachable) {
                    gate.setVisibility(View.GONE);
                    web.setVisibility(View.VISIBLE);
                    if (!webLoaded) {
                        web.loadUrl(dashUrl());
                        webLoaded = true;
                    }
                } else {
                    web.setVisibility(View.GONE);
                    gate.setVisibility(View.VISIBLE);
                    gateStatus.setText(vpn
                            ? "Tailscale looks active, but the dashboard is unreachable.\nIs the homelab up?"
                            : "Dashboard unreachable.\nConnect Tailscale (or join home wifi) and retry.");
                }
            });
        });
    }

    /** GET {url}/healthz with a short timeout, trusting the pinned host's self-signed cert. */
    private boolean probeDashboard() {
        try {
            URL u = new URL(dashUrl() + "/healthz");
            HttpURLConnection c = (HttpURLConnection) u.openConnection();
            if (c instanceof HttpsURLConnection) {
                HttpsURLConnection hc = (HttpsURLConnection) c;
                SSLContext ctx = SSLContext.getInstance("TLS");
                ctx.init(null, new TrustManager[]{new X509TrustManager() {
                    public void checkClientTrusted(X509Certificate[] chain, String authType) {}
                    public void checkServerTrusted(X509Certificate[] chain, String authType) {}
                    public X509Certificate[] getAcceptedIssuers() { return new X509Certificate[0]; }
                }}, new java.security.SecureRandom());
                hc.setSSLSocketFactory(ctx.getSocketFactory());
                hc.setHostnameVerifier((h, sess) -> h.equals(dashHost()));
            }
            c.setConnectTimeout(2500);
            c.setReadTimeout(2500);
            int code = c.getResponseCode();
            c.disconnect();
            return code == 200;
        } catch (Exception e) {
            return false;
        }
    }

    /**
     * Tailscale heuristic: an active VPN transport, or any interface holding a
     * CGNAT (100.64.0.0/10) address — the range Tailscale assigns.
     */
    private boolean tailscaleActive() {
        try {
            ConnectivityManager cm = (ConnectivityManager) getSystemService(Context.CONNECTIVITY_SERVICE);
            for (Network n : cm.getAllNetworks()) {
                NetworkCapabilities caps = cm.getNetworkCapabilities(n);
                if (caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) return true;
            }
            for (NetworkInterface ni : Collections.list(NetworkInterface.getNetworkInterfaces())) {
                for (InetAddress a : Collections.list(ni.getInetAddresses())) {
                    byte[] b = a.getAddress();
                    if (b.length == 4 && (b[0] & 0xFF) == 100 && (b[1] & 0xC0) == 64) return true;
                }
            }
        } catch (Exception ignored) {}
        return false;
    }

    /** Re-probe automatically when connectivity changes (e.g. VPN comes up). */
    private void watchNetwork() {
        ConnectivityManager cm = (ConnectivityManager) getSystemService(Context.CONNECTIVITY_SERVICE);
        netCallback = new ConnectivityManager.NetworkCallback() {
            @Override
            public void onAvailable(Network network) {
                main.postDelayed(() -> {
                    if (gate.getVisibility() == View.VISIBLE) probeAndRoute();
                }, 600);
            }
        };
        cm.registerDefaultNetworkCallback(netCallback);
    }

    @Override
    public void onBackPressed() {
        if (web.getVisibility() == View.VISIBLE && web.canGoBack()) web.goBack();
        else super.onBackPressed();
    }

    @Override
    protected void onResume() {
        super.onResume();
        if (gate.getVisibility() == View.VISIBLE) probeAndRoute();
    }

    @Override
    protected void onDestroy() {
        if (netCallback != null) {
            ((ConnectivityManager) getSystemService(Context.CONNECTIVITY_SERVICE))
                    .unregisterNetworkCallback(netCallback);
        }
        exec.shutdownNow();
        super.onDestroy();
    }
}
