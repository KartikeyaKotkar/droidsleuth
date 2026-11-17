package io.github.droidsleuth.validation;

import android.app.Activity;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.widget.TextView;

/** Disposable process-lifecycle fixture: no permissions, services or user data. */
public final class MainActivity extends Activity {
    @Override
    public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        TextView text = new TextView(this);
        text.setText("DroidSleuth disposable validation\nNo permissions. No user data.");
        text.setPadding(32, 64, 32, 32);
        setContentView(text);
        String action = getIntent().getStringExtra("action");
        if ("java".equals(action)) {
            new Handler(Looper.getMainLooper()).postDelayed(() -> {
                throw new IllegalStateException("DroidSleuth controlled Java crash");
            }, 3000);
        } else if ("native".equals(action)) {
            new Handler(Looper.getMainLooper()).postDelayed(() -> {
                android.os.Process.sendSignal(android.os.Process.myPid(), 11);
            }, 3000);
        }
    }
}
