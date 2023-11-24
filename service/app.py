import os
from datetime import datetime, timezone, timedelta
import bcrypt
import jwt
from functools import wraps
from flask import Flask, request, jsonify, abort, g
from flask_sqlalchemy import SQLAlchemy
from sqlalchemy.exc import IntegrityError

app = Flask(__name__)
app.config["SQLALCHEMY_DATABASE_URI"] = os.getenv(
    "SQLALCHEMY_DATABASE_URI", "sqlite:///chat.db"
)
app.config["SQLALCHEMY_TRACK_MODIFICATIONS"] = False
app.config["JWT_SECRET_KEY"] = os.getenv(
    "JWT_SECRET_KEY", "your-secret-key-zzkzkzkazkz"
)
app.config["JWT_EXP_DELTA_SECONDS"] = 3600  # 1 hour
db = SQLAlchemy(app)


# Models
class User(db.Model):
    id = db.Column(db.Integer, primary_key=True)
    email = db.Column(db.String(255), unique=True, nullable=False)
    password_hash = db.Column(db.String(255), nullable=False)
    username = db.Column(db.String(255), unique=True, nullable=False)
    created_timestamp = db.Column(db.DateTime, default=datetime.now(timezone.utc))
    chats = db.relationship("Chat", backref="user", lazy=True)
    messages = db.relationship("Message", backref="user", lazy=True)

    def to_dict(self):
        return {
            "id": self.id,
            "email": self.email,
            "username": self.username,
            "created_timestamp": self.created_timestamp.isoformat(),
        }

    def set_password(self, password):
        self.password_hash = bcrypt.hashpw(
            password.encode("utf-8"), bcrypt.gensalt()
        ).decode("utf-8")

    def check_password(self, password):
        return bcrypt.hashpw(
            password.encode("utf-8"), self.password_hash.encode("utf-8")
        ) == self.password_hash.encode("utf-8")


class Chat(db.Model):
    id = db.Column(db.Integer, primary_key=True)
    user_id = db.Column(db.Integer, db.ForeignKey("user.id"), nullable=False)
    summary = db.Column(db.String(1024), nullable=False, default="")
    model = db.Column(db.String(1024), nullable=False)
    status = db.Column(db.String(50), nullable=False, default="idle")
    llm_params = db.Column(db.Text, nullable=False, default="{}")
    sampling_params = db.Column(db.Text, nullable=False, default="{}")
    created_timestamp = db.Column(
        db.DateTime, default=lambda: datetime.now(timezone.utc)
    )
    messages = db.relationship(
        "Message", backref="chat", lazy=True, cascade="all, delete-orphan"
    )

    def to_dict(self):
        return {
            "id": self.id,
            "user_id": self.user_id,
            "summary": self.summary,
            "model": self.model,
            "status": self.status,
            "llm_params": self.llm_params,
            "sampling_params": self.sampling_params,
            "created_timestamp": self.created_timestamp.isoformat(),
            # "messages": [message.to_dict() for message in self.messages],
        }


class Message(db.Model):
    id = db.Column(db.Integer, primary_key=True)
    user_id = db.Column(db.Integer, db.ForeignKey("user.id"), nullable=False)
    chat_id = db.Column(
        db.Integer, db.ForeignKey("chat.id", ondelete="CASCADE"), nullable=False
    )
    text = db.Column(db.Text, nullable=False)
    sender_type = db.Column(db.String(50), nullable=False)
    timestamp = db.Column(db.DateTime, default=lambda: datetime.now(timezone.utc))

    def to_dict(self):
        return {
            "id": self.id,
            "user_id": self.user_id,
            "chat_id": self.chat_id,
            "text": self.text,
            "sender_type": self.sender_type,
            "timestamp": self.timestamp.isoformat(),
        }


# Create the database tables
with app.app_context():
    db.create_all()


# Helper functions
def paginate_query(query):
    page = request.args.get("page", 1, type=int)
    per_page = request.args.get("per_page", 100, type=int)
    paginated_query = query.paginate(page=page, per_page=per_page, error_out=False)
    return {
        "items": [item.to_dict() for item in paginated_query.items],
        "total": paginated_query.total,
        "pages": paginated_query.pages,
        "page": page,
    }


def order_query(query, field, default_order):
    order = request.args.get("order", default_order, type=str)
    if order == "desc":
        return query.order_by(field.desc())
    return query.order_by(field.asc())


def validate_message_data(data, required_fields):
    if not data:
        abort(400, description="Request data is missing.")
    for field in required_fields:
        if field not in data:
            abort(400, description=f"Missing '{field}' in request data.")


def token_required(f):
    @wraps(f)
    def decorated_function(*args, **kwargs):
        token = request.headers.get("Authorization")
        if not token:
            return jsonify({"message": "Token is missing!"}), 403

        try:
            payload = jwt.decode(
                token, app.config["JWT_SECRET_KEY"], algorithms=["HS256"]
            )
            current_user = User.query.get(payload["user_id"])
        except jwt.ExpiredSignatureError:
            return jsonify({"message": "Expired token"}), 403
        except jwt.InvalidTokenError:
            return jsonify({"message": "Invalid token"}), 403

        g.user = current_user
        return f(*args, **kwargs)

    return decorated_function


def make_auth_token():
    payload = {
        "user_id": g.user.id,
        "exp": datetime.utcnow()
        + timedelta(seconds=app.config["JWT_EXP_DELTA_SECONDS"]),
    }
    return jwt.encode(payload, app.config["JWT_SECRET_KEY"], algorithm="HS256")


# API Endpoints
@app.route("/register", methods=["POST"])
def register_user():
    data = request.get_json()
    if (
        not data
        or not data.get("email")
        or not data.get("password")
        or not data.get("username")
    ):
        return jsonify({"error": "Missing registration information"}), 400

    user = User(email=data["email"], username=data["username"])
    user.set_password(data["password"])
    try:
        db.session.add(user)
        db.session.commit()
        g.user = user
    except IntegrityError:
        db.session.rollback()
        return (
            jsonify({"error": "User with that email or username already exists"}),
            409,
        )

    return jsonify(dict(user.to_dict(), token=make_auth_token())), 201


@app.route("/login", methods=["POST"])
def login_user():
    data = request.get_json()
    if not data or not data.get("email") or not data.get("password"):
        return jsonify({"error": "Missing login credentials"}), 400

    user = User.query.filter_by(email=data["email"]).first()
    if user and user.check_password(data["password"]):
        g.user = user
        return jsonify({"token": make_auth_token()}), 200
    return jsonify({"error": "Invalid credentials"}), 401


@app.route("/auth/user", methods=["GET"])
@token_required
def auth_user():
    return jsonify(g.user.to_dict()), 200


@app.route("/chats", methods=["GET"])
@token_required
def get_chats():
    chats_query = order_query(
        Chat.query.filter_by(user_id=g.user.id), Chat.created_timestamp, "desc"
    )
    return jsonify(paginate_query(chats_query))


@app.route("/chats", methods=["POST"])
@token_required
def create_chat():
    data = request.get_json()
    if not data or not data.get("model"):
        return jsonify({"error": "Missing chat model information"}), 400

    chat = Chat(
        user_id=g.user.id,
        model=data["model"],
        llm_params=data.get("llm_params", "{}"),
        sampling_params=data.get("sampling_params", "{}"),
    )
    db.session.add(chat)
    db.session.commit()
    return jsonify(chat.to_dict()), 201


@app.route("/chats/<int:chat_id>", methods=["PUT"])
@token_required
def update_chat(chat_id):
    chat = Chat.query.get_or_404(chat_id)
    if chat.user_id != g.user.id:
        return jsonify({"error": "Unauthorized"}), 403

    data = request.get_json()
    if not data:
        abort(400, description="Request data is missing.")

    if "summary" in data:
        chat.summary = data["summary"][:1024]
    if "status" in data:
        chat.status = data["status"]
    if "llm_params" in data:
        chat.llm_params = data["llm_params"]
    if "sampling_params" in data:
        chat.sampling_params = data["sampling_params"]

    db.session.commit()
    return jsonify(chat.to_dict())


@app.route("/chats/<int:chat_id>", methods=["GET"])
@token_required
def get_chat(chat_id):
    chat = Chat.query.get_or_404(chat_id)
    if chat.user_id != g.user.id:
        return jsonify({"error": "Unauthorized"}), 403

    return jsonify(chat.to_dict())


@app.route("/chats/<int:chat_id>", methods=["DELETE"])
@token_required
def delete_chat(chat_id):
    chat = Chat.query.get_or_404(chat_id)
    if chat.user_id != g.user.id:
        return jsonify({"error": "Unauthorized"}), 403

    db.session.delete(chat)
    db.session.commit()
    return jsonify({"message": "Chat deleted"}), 200


@app.route("/chats/<int:chat_id>/messages", methods=["GET"])
@token_required
def get_messages(chat_id):
    chat = Chat.query.get_or_404(chat_id)
    if chat.user_id != g.user.id:
        return jsonify({"error": "Unauthorized"}), 403

    messages_query = order_query(
        Message.query.filter_by(chat_id=chat_id), Message.timestamp, "desc"
    )
    return jsonify(paginate_query(messages_query))


@app.route("/chats/<int:chat_id>/messages", methods=["POST"])
@token_required
def create_message(chat_id):
    chat = Chat.query.get_or_404(chat_id)
    if chat.user_id != g.user.id:
        return jsonify({"error": "Unauthorized"}), 403

    data = request.get_json()
    validate_message_data(data, ["text", "sender_type"])
    message = Message(
        chat_id=chat_id,
        user_id=g.user.id,
        text=data["text"],
        sender_type=data["sender_type"],
    )

    if not chat.summary:
        chat.summary = data["text"][:1024]

    db.session.add(message)
    db.session.commit()
    return jsonify(message.to_dict()), 201


@app.route("/messages/<int:message_id>", methods=["GET"])
@token_required
def get_message(message_id):
    message = Message.query.get_or_404(message_id)
    if message.user_id != g.user.id:
        return jsonify({"error": "Unauthorized"}), 403

    return jsonify(message.to_dict())


@app.route("/messages/<int:message_id>", methods=["PUT"])
@token_required
def update_message(message_id):
    message = Message.query.get_or_404(message_id)
    if message.user_id != g.user.id:
        return jsonify({"error": "Unauthorized"}), 403

    data = request.get_json()
    validate_message_data(data, ["text"])
    message.text = data["text"]
    db.session.commit()
    return jsonify(message.to_dict())


@app.route("/messages/<int:message_id>", methods=["DELETE"])
@token_required
def delete_message(message_id):
    message = Message.query.get_or_404(message_id)
    if message.user_id != g.user.id:
        return jsonify({"error": "Unauthorized"}), 403

    db.session.delete(message)
    db.session.commit()
    return jsonify({"message": "Message deleted"}), 200


# Error handling
@app.errorhandler(400)
def bad_request(error):
    return jsonify({"error": "Bad request", "message": error.description}), 400


@app.errorhandler(404)
def not_found(error):
    return jsonify({"error": "Not found"}), 404


@app.route("/<path:path>", methods=["OPTIONS"])
def handle_options(path):
    response = app.make_default_options_response()
    response.headers.add("Access-Control-Allow-Origin", "*")
    response.headers.add(
        "Access-Control-Allow-Headers",
        "Content-Type,Authorization,Origin,X-Requested-With,Accept,Accept-Language,Content-Language",
    )
    response.headers.add("Access-Control-Allow-Methods", "GET,PUT,POST,DELETE,OPTIONS")
    return response


# This is how we allow all origins to access our API
# This is not secure and should not be used in production
@app.after_request
def after_request(response):
    response.headers.add("Access-Control-Allow-Origin", "*")
    response.headers.add(
        "Access-Control-Allow-Headers",
        "Content-Type,Authorization,Origin,X-Requested-With,Accept,Accept-Language,Content-Language",
    )
    response.headers.add("Access-Control-Allow-Methods", "GET,PUT,POST,DELETE,OPTIONS")
    return response


if __name__ == "__main__":
    app.run(
        host="0.0.0.0", port=7025, debug=False
    )  # Turn off debug mode for production
