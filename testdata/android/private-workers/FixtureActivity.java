package io.github.droidsleuth.v03;

import android.app.Activity;
import android.content.*;
import android.os.*;
import android.widget.TextView;

public final class FixtureActivity extends Activity {
    private final ServiceConnection connection = new ServiceConnection() {
        public void onServiceConnected(ComponentName name, IBinder binder) {}
        public void onServiceDisconnected(ComponentName name) {}
    };

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        TextView label = new TextView(this);
        label.setText("DroidSleuth v0.3 disposable process fixture");
        setContentView(label);
        if (getPackageName().equals("io.github.droidsleuth.v03")) {
            bindService(new Intent(this, Isolated.class), connection, Context.BIND_AUTO_CREATE);
        }
    }
}
