package io.github.droidsleuth.validation;

import android.app.Activity;
import android.os.Bundle;
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
    }
}
