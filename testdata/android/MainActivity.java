package io.github.droidsleuth.validation;

import android.app.Activity;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.os.SystemClock;
import java.nio.ByteBuffer;
import android.widget.TextView;

/** Disposable process-lifecycle fixture: no permissions, services or user data. */
public final class MainActivity extends Activity {
    private ByteBuffer resident;
    private static volatile long workloadSink;

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
        } else if ("resources".equals(action)) {
            Handler handler = new Handler(Looper.getMainLooper());
            handler.postDelayed(() -> {
                resident = ByteBuffer.allocateDirect(32 * 1024 * 1024);
                for (int offset = 0; offset < resident.capacity(); offset += 4096) {
                    resident.put(offset, (byte) 1);
                }
            }, 4000);
            handler.postDelayed(() -> burnCPU(1, 12000), 8000);
            handler.postDelayed(() -> burnCPU(2, 12000), 22000);
        } else if ("native".equals(action)) {
            new Handler(Looper.getMainLooper()).postDelayed(() -> {
                android.os.Process.sendSignal(android.os.Process.myPid(), 11);
            }, 3000);
        }
    }
    // Workers stop on their own and never block the activity's main thread.
    private static void burnCPU(int threads, long millis) {
        long deadline = SystemClock.elapsedRealtime() + millis;
        for (int i = 0; i < threads; i++) {
            new Thread(() -> {
                long value = 1;
                while (SystemClock.elapsedRealtime() < deadline) {
                    for (int j = 0; j < 4096; j++) {
                        value = value * 1664525 + 1013904223;
                    }
                    workloadSink = value;
                }
            }, "droidsleuth-fixture-cpu").start();
        }
    }
}
